// Command casamod-store serves the CasaMOD Store web UI and keeps mods and
// wallpapers installed from GitHub up to date.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nescauquente/casamod-store/internal/app"
	"github.com/nescauquente/casamod-store/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags)
	delay, _ := strconv.ParseFloat(env("CASAMOD_STORE_RESTART_DELAY", "3"), 64)
	a, err := app.New(app.Options{
		DataDir:      env("CASAMOD_STORE_DATA", "/DATA/AppData/casamod-store"),
		CasaMODDir:   env("CASAMOD_DIR", "/DATA/AppData/casamod"),
		DockerSocket: env("DOCKER_SOCKET", "/var/run/docker.sock"),
		Version:      version,
		RestartDelay: time.Duration(delay * float64(time.Second)),
	})
	if err != nil {
		log.Fatal(err)
	}
	// Test hooks: point the GitHub client at a mock server.
	if v := os.Getenv("GITHUB_API_URL"); v != "" {
		a.GH.APIBase = v
	}
	if v := os.Getenv("GITHUB_CODELOAD_URL"); v != "" {
		a.GH.CodeloadBase = v
	}
	if v := os.Getenv("GITHUB_RAW_URL"); v != "" {
		a.GH.RawBase = v
	}

	a.EnsureNotifier()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go a.Run(ctx)

	addr := env("CASAMOD_STORE_ADDR", ":8418")
	srv := &http.Server{Addr: addr, Handler: a.Handler(web.FS), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	a.Logf("info", "CasaMOD Store %s listening on %s", version, addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
