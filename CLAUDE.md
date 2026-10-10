# CasaMOD Store

Container for CasaOS that installs, updates and removes CasaMOD mods and
wallpapers from GitHub, with a web UI and a notifier mini-mod. See README.md.

## Rules
- Reply to the user in Portuguese (pt-BR); code, comments and docs in English.
- The user's CasaOS (`http://the-maquina`, CasaOS 0.4.15) is only reachable on
  their LAN/Tailscale: test against the mocks, never against the real server.
- Commits as NescauQuente; no PR unless asked.
- On the user's (weak) PC run the browser suite once per change; in the cloud
  it can run every time.

## Layout
- Go, stdlib only (`go.mod` has no requirements). Keep it that way.
- `internal/app`: install/update/remove, checks, HTTP API (`server.go`).
- `internal/installer`: tarball extraction + atomic swap (`.casamod-store-*`
  dot folders, ignored by CasaMOD's `mod/*` glob).
- `internal/notifier/files`: the mini-mod; its texts cover the 31 CasaOS
  languages (`src/assets/lang` of CasaOS-UI, lowercase keys like `pt_br`).
- `web/static`: plain HTML/CSS/JS, embedded; texts in `en` and `pt`.

## Tests
- `go test -race ./...` (fake GitHub in `internal/fakegithub`, fake Docker socket).
- `node tests/run.mjs` (no npm deps; CDP harness in `tests/harness.mjs`;
  `CHROME_BIN` to pick the browser; `SCREENSHOTS=dir` saves screenshots).

## CasaMOD facts this relies on
- CasaMOD injects `mod/<dir>/mod.js|mod.css` into CasaOS' index.html at start;
  new/removed mods need a `casamod` container restart, updated files do not.
- `/DATA/AppData/casamod/mod` is served at `/mod/`; the notifier reads
  `/mod/casamod-store-notifier/status.json` written by the store.
- Live Wallpapers reads wallpapers from `mod/wallpapers/<id>`.
