// Package github talks to the GitHub REST API and codeload to find the latest
// commit of a repository (or of a sub folder) and to download its contents.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Source points at a folder inside a GitHub repository.
type Source struct {
	Repo   string `json:"repo"`             // "owner/name"
	Branch string `json:"branch,omitempty"` // empty means the default branch
	Path   string `json:"path,omitempty"`   // sub folder, empty means the repository root
}

// String returns a human readable form such as "owner/name@main:path".
func (s Source) String() string {
	out := s.Repo
	if s.Branch != "" {
		out += "@" + s.Branch
	}
	if s.Path != "" {
		out += ":" + s.Path
	}
	return out
}

// Validate checks that the repository looks like "owner/name" and that the
// path cannot escape the repository.
func (s Source) Validate() error {
	if !repoRe.MatchString(s.Repo) {
		return fmt.Errorf("invalid repository %q (expected owner/name)", s.Repo)
	}
	if s.Branch != "" && (strings.Contains(s.Branch, "..") || strings.ContainsAny(s.Branch, " ~^:?*[\\")) {
		return fmt.Errorf("invalid branch %q", s.Branch)
	}
	for _, part := range strings.Split(s.Path, "/") {
		if part == ".." {
			return fmt.Errorf("invalid path %q", s.Path)
		}
	}
	return nil
}

// Normalize trims slashes and whitespace.
func (s Source) Normalize() Source {
	s.Repo = strings.Trim(strings.TrimSpace(s.Repo), "/")
	s.Repo = strings.TrimSuffix(s.Repo, ".git")
	s.Branch = strings.TrimSpace(s.Branch)
	s.Path = strings.Trim(strings.TrimSpace(s.Path), "/")
	return s
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ErrNotFound is returned when the repository, branch or path does not exist
// (or is private and no valid token was configured).
var ErrNotFound = errors.New("not found on GitHub (wrong repository/branch/path, or a private repository without a token)")

// ErrRateLimited is returned when GitHub refuses the request because of rate limits.
var ErrRateLimited = errors.New("GitHub API rate limit reached (add a token in Settings to raise the limit)")

// Client is a small GitHub client. The zero value is not usable, use New.
type Client struct {
	APIBase      string // e.g. https://api.github.com
	CodeloadBase string // e.g. https://codeload.github.com
	RawBase      string // e.g. https://raw.githubusercontent.com
	Token        func() string
	HTTP         *http.Client
	UserAgent    string
}

// New returns a client for github.com.
func New(token func() string) *Client {
	return &Client{
		APIBase:      "https://api.github.com",
		CodeloadBase: "https://codeload.github.com",
		RawBase:      "https://raw.githubusercontent.com",
		Token:        token,
		HTTP:         &http.Client{Timeout: 10 * time.Minute},
		UserAgent:    "casamod-store",
	}
}

func (c *Client) token() string {
	if c.Token == nil {
		return ""
	}
	return strings.TrimSpace(c.Token())
}

func (c *Client) newRequest(ctx context.Context, rawURL string, api bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	if t := c.token(); t != "" && (api || strings.HasPrefix(rawURL, c.RawBase)) {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, ErrRateLimited
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("GitHub rejected the token (401): check it in Settings")
	case resp.StatusCode == http.StatusUnprocessableEntity:
		// Unknown branch on the commits endpoint.
		return nil, ErrNotFound
	}
	msg := strings.TrimSpace(string(body))
	var apiErr struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
		msg = apiErr.Message
	}
	return nil, fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, msg)
}

func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := c.newRequest(ctx, rawURL, true)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// DefaultBranch returns the default branch of a repository.
func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.getJSON(ctx, c.APIBase+"/repos/"+repo, &info); err != nil {
		return "", err
	}
	if info.DefaultBranch == "" {
		return "", errors.New("GitHub did not report a default branch")
	}
	return info.DefaultBranch, nil
}

// Commit is the latest commit that touched a source.
type Commit struct {
	SHA     string    `json:"sha"`
	Date    time.Time `json:"date"`
	Message string    `json:"message"`
}

// LatestCommit returns the most recent commit on the branch that touched the
// source path (or the branch head when the path is empty).
func (c *Client) LatestCommit(ctx context.Context, src Source) (Commit, error) {
	q := url.Values{}
	if src.Branch != "" {
		q.Set("sha", src.Branch)
	}
	if src.Path != "" {
		q.Set("path", src.Path)
	}
	q.Set("per_page", "1")
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message   string `json:"message"`
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.getJSON(ctx, c.APIBase+"/repos/"+src.Repo+"/commits?"+q.Encode(), &commits); err != nil {
		return Commit{}, err
	}
	if len(commits) == 0 {
		// The commits endpoint returns an empty list when the path does not exist.
		return Commit{}, ErrNotFound
	}
	msg := commits[0].Commit.Message
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return Commit{SHA: commits[0].SHA, Date: commits[0].Commit.Committer.Date, Message: msg}, nil
}

// Tarball opens a gzipped tarball of the repository at the given commit.
// Without a token codeload is used directly (it is not rate limited); with a
// token the API endpoint is used so that private repositories work.
func (c *Client) Tarball(ctx context.Context, repo, ref string) (io.ReadCloser, error) {
	var rawURL string
	api := c.token() != ""
	if api {
		rawURL = c.APIBase + "/repos/" + repo + "/tarball/" + url.PathEscape(ref)
	} else {
		rawURL = c.CodeloadBase + "/" + repo + "/tar.gz/" + url.PathEscape(ref)
	}
	req, err := c.newRequest(ctx, rawURL, api)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// RawFile downloads a single file from a repository. With a token the API is
// used (private repositories), otherwise raw.githubusercontent.com.
func (c *Client) RawFile(ctx context.Context, repo, ref, path string, limit int64) ([]byte, error) {
	var req *http.Request
	var err error
	if c.token() != "" {
		q := ""
		if ref != "" {
			q = "?ref=" + url.QueryEscape(ref)
		}
		req, err = c.newRequest(ctx, c.APIBase+"/repos/"+repo+"/contents/"+escapePath(path)+q, true)
		if err == nil {
			req.Header.Set("Accept", "application/vnd.github.raw")
		}
	} else {
		if ref == "" {
			ref = "HEAD"
		}
		req, err = c.newRequest(ctx, c.RawBase+"/"+repo+"/"+escapePath(ref)+"/"+escapePath(path), false)
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readLimited(resp.Body, limit)
}

// Fetch downloads an arbitrary URL (used for catalogs hosted outside GitHub).
func (c *Client) Fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := c.newRequest(ctx, rawURL, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readLimited(resp.Body, limit)
}

// TreeEntry is one entry of a recursive git tree listing.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" or "tree"
}

// Tree lists every file and folder of a branch in one request.
func (c *Client) Tree(ctx context.Context, repo, ref string) ([]TreeEntry, error) {
	var out struct {
		Tree []TreeEntry `json:"tree"`
	}
	if err := c.getJSON(ctx, c.APIBase+"/repos/"+repo+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", &out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file is larger than %d bytes", limit)
	}
	return data, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// ParseURL understands the GitHub URLs people usually paste:
//
//	https://github.com/owner/repo
//	https://github.com/owner/repo/tree/branch/sub/folder
//	https://github.com/owner/repo/blob/branch/sub/folder/file
//	github.com/owner/repo.git
//	owner/repo
//
// Branch names containing "/" are ambiguous in tree URLs; the first segment
// is taken as the branch.
func ParseURL(raw string) (Source, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "github.com/") {
		s = strings.TrimPrefix(s, "github.com/")
	} else if strings.Contains(strings.SplitN(s, "/", 2)[0], ".") {
		return Source{}, fmt.Errorf("not a GitHub URL: %q", raw)
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("not a GitHub repository URL: %q", raw)
	}
	src := Source{Repo: parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")}
	if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
		src.Branch = parts[3]
		src.Path = strings.Join(parts[4:], "/")
		if parts[2] == "blob" {
			// A file was pasted: use its folder.
			if i := strings.LastIndex(src.Path, "/"); i >= 0 {
				src.Path = src.Path[:i]
			} else {
				src.Path = ""
			}
		}
	} else if len(parts) > 2 {
		return Source{}, fmt.Errorf("unsupported GitHub URL: %q", raw)
	}
	if unescaped, err := url.PathUnescape(src.Path); err == nil {
		src.Path = unescaped
	}
	src = src.Normalize()
	return src, src.Validate()
}
