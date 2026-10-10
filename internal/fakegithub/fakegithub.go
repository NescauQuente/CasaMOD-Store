// Package fakegithub is an in-memory stand-in for the parts of GitHub the
// store uses (REST API, codeload and raw). It backs the Go tests and the
// browser tests (see tests/fakegithub).
package fakegithub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// Commit is a snapshot of a branch.
type Commit struct {
	SHA     string
	Message string
	Date    time.Time
	Files   map[string]string // full tree after the commit
	Touched []string          // paths changed by the commit
}

// Repo is a fake repository with a single history per branch.
type Repo struct {
	DefaultBranch string
	Private       bool
	Branches      map[string][]*Commit // oldest first
}

// Server holds the repositories and counts requests.
type Server struct {
	mu       sync.Mutex
	Repos    map[string]*Repo
	Token    string // token accepted for private repositories
	Requests []string
	// Runs holds successful workflow runs per "repo|workflow|branch", oldest first.
	Runs map[string][]string
	// RateLimited makes every API call fail with 403 + X-RateLimit-Remaining: 0.
	RateLimited bool
}

// New returns an empty server.
func New() *Server { return &Server{Repos: map[string]*Repo{}, Runs: map[string][]string{}} }

// AddRun records a successful workflow run for a commit.
func (s *Server) AddRun(repo, workflow, branch, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := repo + "|" + workflow + "|" + branch
	s.Runs[k] = append(s.Runs[k], sha)
}

// Push adds a commit to a branch: files replace (or with "" delete) paths.
func (s *Server) Push(repo, branch, message string, files map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.Repos[repo]
	if r == nil {
		r = &Repo{DefaultBranch: branch, Branches: map[string][]*Commit{}}
		s.Repos[repo] = r
	}
	tree := map[string]string{}
	hist := r.Branches[branch]
	if len(hist) > 0 {
		for k, v := range hist[len(hist)-1].Files {
			tree[k] = v
		}
	}
	var touched []string
	for k, v := range files {
		touched = append(touched, k)
		if v == "" {
			delete(tree, k)
		} else {
			tree[k] = v
		}
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d|%s|%v", repo, branch, len(hist), message, files)))
	c := &Commit{
		SHA: hex.EncodeToString(sum[:]), Message: message, Files: tree, Touched: touched,
		Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(len(hist)) * time.Hour),
	}
	r.Branches[branch] = append(hist, c)
	return c.SHA
}

// SetPrivate marks a repository as private.
func (s *Server) SetPrivate(repo string, private bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Repos[repo].Private = private
}

// Count returns how many requests matched a prefix.
func (s *Server) Count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// Start runs the server; API, codeload and raw share one host, under
// /api, /codeload and /raw.
func (s *Server) Start() *httptest.Server { return httptest.NewServer(s) }

func (s *Server) find(repo, ref string) (*Repo, *Commit) {
	r := s.Repos[repo]
	if r == nil {
		return nil, nil
	}
	if ref == "" || ref == "HEAD" {
		ref = r.DefaultBranch
	}
	if hist := r.Branches[ref]; len(hist) > 0 {
		return r, hist[len(hist)-1]
	}
	for _, hist := range r.Branches {
		for _, c := range hist {
			if c.SHA == ref {
				return r, c
			}
		}
	}
	return r, nil
}

func (s *Server) authorized(r *Repo, req *http.Request) bool {
	if r == nil || !r.Private {
		return true
	}
	return s.Token != "" && req.Header.Get("Authorization") == "Bearer "+s.Token
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/__push" && req.Method == http.MethodPost {
		// Test control endpoint: {"repo","branch","message","files"}.
		var body struct {
			Repo, Branch, Message string
			Files                 map[string]string
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"sha": s.Push(body.Repo, body.Branch, body.Message, body.Files)})
		return
	}
	if req.URL.Path == "/__run" && req.Method == http.MethodPost {
		// Test control endpoint: {"repo","workflow","branch","sha"}.
		var body struct{ Repo, Workflow, Branch, SHA string }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.AddRun(body.Repo, body.Workflow, body.Branch, body.SHA)
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, req.URL.Path+"?"+req.URL.RawQuery)
	p := req.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/"):
		if s.RateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		s.api(w, req, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/codeload/"):
		// /codeload/{o}/{r}/tar.gz/{ref}
		parts := strings.Split(strings.TrimPrefix(p, "/codeload/"), "/")
		if len(parts) != 4 || parts[2] != "tar.gz" {
			http.NotFound(w, req)
			return
		}
		repo := parts[0] + "/" + parts[1]
		r, c := s.find(repo, parts[3])
		if c == nil || r.Private {
			http.NotFound(w, req)
			return
		}
		s.tarball(w, repo, c)
	case strings.HasPrefix(p, "/raw/"):
		// /raw/{o}/{r}/{ref}/{path}
		parts := strings.SplitN(strings.TrimPrefix(p, "/raw/"), "/", 4)
		if len(parts) != 4 {
			http.NotFound(w, req)
			return
		}
		r, c := s.find(parts[0]+"/"+parts[1], parts[2])
		if c == nil || r.Private {
			http.NotFound(w, req)
			return
		}
		data, ok := c.Files[parts[3]]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write([]byte(data))
	default:
		http.NotFound(w, req)
	}
}

func (s *Server) api(w http.ResponseWriter, req *http.Request, p string) {
	parts := strings.Split(strings.TrimPrefix(p, "/repos/"), "/")
	if !strings.HasPrefix(p, "/repos/") || len(parts) < 2 {
		http.NotFound(w, req)
		return
	}
	repo := parts[0] + "/" + parts[1]
	r := s.Repos[repo]
	if r == nil || !s.authorized(r, req) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	rest := parts[2:]
	switch {
	case len(rest) == 0:
		writeJSON(w, map[string]any{"full_name": repo, "default_branch": r.DefaultBranch, "private": r.Private})
	case rest[0] == "commits" && len(rest) == 1:
		ref := req.URL.Query().Get("sha")
		if ref == "" {
			ref = r.DefaultBranch
		}
		hist := r.Branches[ref]
		if hist == nil {
			http.Error(w, `{"message":"No commit found for SHA"}`, http.StatusNotFound)
			return
		}
		path := strings.Trim(req.URL.Query().Get("path"), "/")
		out := []any{}
		for i := len(hist) - 1; i >= 0; i-- {
			c := hist[i]
			if path == "" || touches(c, path) {
				out = append(out, map[string]any{
					"sha": c.SHA,
					"commit": map[string]any{
						"message":   c.Message,
						"committer": map[string]any{"date": c.Date.Format(time.RFC3339)},
					},
				})
				break
			}
		}
		writeJSON(w, out)
	case rest[0] == "actions" && len(rest) == 4 && rest[1] == "workflows" && rest[3] == "runs":
		runs := s.Runs[repo+"|"+rest[2]+"|"+req.URL.Query().Get("branch")]
		out := []any{}
		if len(runs) > 0 {
			out = append(out, map[string]any{"head_sha": runs[len(runs)-1], "updated_at": "2026-02-01T00:00:00Z", "display_title": "publish"})
		}
		writeJSON(w, map[string]any{"total_count": len(out), "workflow_runs": out})
	case rest[0] == "tarball" && len(rest) == 2:
		_, c := s.find(repo, rest[1])
		if c == nil {
			http.NotFound(w, req)
			return
		}
		s.tarball(w, repo, c)
	case rest[0] == "git" && len(rest) == 3 && rest[1] == "trees":
		_, c := s.find(repo, rest[2])
		if c == nil {
			http.NotFound(w, req)
			return
		}
		seen := map[string]bool{}
		var tree []map[string]string
		for f := range c.Files {
			tree = append(tree, map[string]string{"path": f, "type": "blob"})
			dirs := strings.Split(f, "/")
			for i := 1; i < len(dirs); i++ {
				d := strings.Join(dirs[:i], "/")
				if !seen[d] {
					seen[d] = true
					tree = append(tree, map[string]string{"path": d, "type": "tree"})
				}
			}
		}
		sort.Slice(tree, func(i, j int) bool { return tree[i]["path"] < tree[j]["path"] })
		writeJSON(w, map[string]any{"tree": tree})
	case rest[0] == "contents":
		_, c := s.find(repo, req.URL.Query().Get("ref"))
		if c == nil {
			http.NotFound(w, req)
			return
		}
		data, ok := c.Files[strings.Join(rest[1:], "/")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.Write([]byte(data))
	default:
		http.NotFound(w, req)
	}
}

func touches(c *Commit, path string) bool {
	for _, t := range c.Touched {
		if t == path || strings.HasPrefix(t, path+"/") {
			return true
		}
	}
	return false
}

func (s *Server) tarball(w http.ResponseWriter, repo string, c *Commit) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := strings.ReplaceAll(repo, "/", "-") + "-" + c.SHA[:7] + "/"
	tw.WriteHeader(&tar.Header{Name: root, Typeflag: tar.TypeDir, Mode: 0o755})
	names := make([]string, 0, len(c.Files))
	for f := range c.Files {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		data := c.Files[f]
		tw.WriteHeader(&tar.Header{Name: root + f, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))})
		tw.Write([]byte(data))
	}
	tw.Close()
	gz.Close()
	w.Header().Set("Content-Type", "application/x-gzip")
	w.Write(buf.Bytes())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
