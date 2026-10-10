// Package docker restarts the CasaMOD container through the Docker Engine API
// on the mounted unix socket. Only the restart and inspect endpoints are used.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Client talks to the Docker daemon over a unix socket.
type Client struct {
	Socket string
	http   *http.Client
}

// New returns a client for the given socket path.
func New(socket string) *Client {
	return &Client{
		Socket: socket,
		http: &http.Client{
			Timeout: 2 * time.Minute,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		},
	}
}

// Available reports whether the socket is mounted.
func (c *Client) Available() bool {
	fi, err := os.Stat(c.Socket)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func (c *Client) call(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return nil, fmt.Errorf("docker: %s (%d)", msg, resp.StatusCode)
	}
	return body, nil
}

// Running reports whether the container exists and is running.
func (c *Client) Running(ctx context.Context, name string) (bool, error) {
	body, err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json")
	if err != nil {
		return false, err
	}
	var info struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return false, err
	}
	return info.State.Running, nil
}

// Restart restarts a container, giving it 10 seconds to stop gracefully
// (CasaMOD restores the CasaOS index.html on SIGTERM).
func (c *Client) Restart(ctx context.Context, name string) error {
	_, err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/restart?t=10")
	return err
}

// Restarter coalesces restart requests: installing five mods in a row
// restarts CasaMOD once, a moment after the last one.
type Restarter struct {
	Docker    *Client
	Container func() string
	Delay     time.Duration
	OnResult  func(err error)

	mu      sync.Mutex
	timer   *time.Timer
	pending bool
}

// ErrNoSocket means the Docker socket is not mounted.
var ErrNoSocket = errors.New("the Docker socket is not mounted, restart CasaMOD manually")

// Request schedules a restart.
func (r *Restarter) Request() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(r.Delay, r.run)
}

// Pending reports whether a restart is scheduled but has not happened yet.
func (r *Restarter) Pending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

// Now restarts immediately and returns the result.
func (r *Restarter) Now(ctx context.Context) error {
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.pending = false
	r.mu.Unlock()
	err := r.restart(ctx)
	if r.OnResult != nil {
		r.OnResult(err)
	}
	return err
}

func (r *Restarter) run() {
	r.mu.Lock()
	r.pending = false
	r.mu.Unlock()
	err := r.restart(context.Background())
	if r.OnResult != nil {
		r.OnResult(err)
	}
}

func (r *Restarter) restart(ctx context.Context) error {
	if r.Docker == nil || !r.Docker.Available() {
		return ErrNoSocket
	}
	return r.Docker.Restart(ctx, r.Container())
}
