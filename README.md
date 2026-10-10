# CasaMOD Store

A store for [CasaMOD](https://github.com/Cp0204/CasaMOD): install, update and
remove CasaMOD **mods** and **wallpapers** straight from GitHub, from a web UI
that opens from the app icon in CasaOS.

- **Store**: items from source repositories (by default
  [CasaMODs](https://github.com/NescauQuente/CasaMODs) and
  [WE-Wallpapers](https://github.com/NescauQuente/WE-Wallpapers)), the mods
  that ship with CasaMOD, or any GitHub repository/folder pasted as a URL.
- **Updates**: checked every 6 hours (configurable) or on demand. Per item you
  choose *Notify me* (default) or *Update automatically*.
- **Desktop notice**: a tiny mini-mod (`casamod-store-notifier`) shows a
  discreet notice on the CasaOS desktop when updates are waiting, with a link
  to the store. Texts in the 31 CasaOS languages.
- **Updates of the store itself**: the image records the commit it was built
  from; each check compares it with the last successful
  `docker-publish.yml` run on `main` and, when a newer image exists, shows it
  in the UI and in the desktop notice. Update it with CasaOS' *Check then
  update*, which works once this repository is added as an app store source
  (see below).
- Small: one static Go binary (no dependencies), multi-arch image
  (amd64, arm/v7, arm64).

## How it works

| Item       | Installed into                                | After install/remove        |
|------------|-----------------------------------------------|-----------------------------|
| mod        | `/DATA/AppData/casamod/mod/<id>`              | CasaMOD is restarted        |
| wallpaper  | `/DATA/AppData/casamod/mod/wallpapers/<id>`   | nothing (used by Live Wallpapers) |

- CasaMOD injects `mod.js`/`mod.css` of each folder of `mod/` into the CasaOS
  page when it starts, so **adding or removing a mod** needs a restart of the
  `casamod` container. The store does it through the Docker socket, once per
  batch. **Updating** an installed mod only replaces its files (served live):
  reload the CasaOS page.
- Downloads happen on the server: the last commit that touched the folder is
  read from the GitHub API, the tarball of that commit is downloaded and only
  the folder is extracted. Installs are **atomic**: files are extracted next
  to the target and swapped in with renames; a failed download never leaves a
  half-written mod, and an interrupted swap is repaired on the next start.
- Folders the store did not install are listed as *Installed manually* and
  are never changed or removed. Installing over one asks first.
- State lives in `/DATA/AppData/casamod-store`: `config.json` (settings and the
  optional GitHub token, mode 0600) and `installed.json` (source, commit and
  update policy of each item).

## Install on CasaOS

This repository is also a CasaOS app store source (`Apps/casamod-store/`).

1. Install CasaMOD first.
2. In CasaOS: **App Store → More apps**, add
   `https://github.com/NescauQuente/CasaMOD-Store/archive/refs/heads/main.zip`.
3. Install **CasaMOD Store** from the App Store and open it from its icon
   (port `8418`).

CasaOS only offers *Check then update* for apps it finds in a source, so
installing from the source is what makes updates work. A custom install of
[`Apps/casamod-store/docker-compose.yml`](Apps/casamod-store/docker-compose.yml)
uses the same `store_app_id` and is picked up as well once the source is
added.

### About the Docker socket

The compose file mounts `/var/run/docker.sock` so the store can restart the
`casamod` container. Access to the socket gives a container full control of
Docker on the host. The store only calls the *restart* endpoint of the
container named in Settings, but if you prefer, delete that volume: the store
keeps working and shows a banner asking you to restart CasaMOD yourself.

The web UI has no login (like most CasaOS apps): keep the port inside your
local network / VPN. State-changing API calls require a custom header, so
other websites cannot drive the store from your browser.

### GitHub token (optional)

Without a token the GitHub API allows 60 requests per hour per IP, which is
enough for a few dozen items. Add a token in **Settings** to install from
private repositories or to raise the limit to 5000/hour. A fine-grained token
with read-only *Contents* access to the repositories you need is enough.

## Sources

A source is a GitHub repository (or a folder of one) added in **Settings**.
Without a `store.json`, the store reads the repository tree (one API call)
and lists:

- every folder with a Wallpaper Engine `project.json` of type `web` as a
  **wallpaper**, named after its title (`wallpaper-engine/` with title
  "Starry Orbit" installs as `wallpapers/starry-orbit`); nothing inside or
  around a project counts as a separate item;
- every other folder with `mod.js` or `mod.css` at its root as a **mod**;
- every other folder with `index.html` as a **wallpaper**;

taking only the outermost match and skipping hidden, `test(s)`, `docs`,
`examples` and `node_modules` folders. Mod ids are folder names; names,
descriptions and previews come from `mod.json` / `project.json`.
New folders appear in the store on their own, and installed items follow
every commit to their folder.

### Optional `store.json`

A `store.json` at the root of a source replaces the scan and lists the
items explicitly:

```json
{
  "name": "My mods",
  "items": [
    {
      "id": "my-mod",
      "name": "My Mod",
      "type": "mod",
      "repo": "owner/repo",
      "branch": "main",
      "path": "mods/my-mod",
      "description": "What it does",
      "preview": "preview.png",
      "author": "me",
      "version": "1.0.0"
    }
  ]
}
```

- `type`: `mod` (default) or `wallpaper`. A mod folder must have `mod.js` or
  `mod.css` at its root (the format of CasaMOD's `mod.json` is welcome too).
- `id`: folder name; defaults to the last segment of `path`.
- `repo`: defaults to the catalog's repository; `branch`: defaults to the
  repository's default branch; `path`: defaults to the repository root.
- `preview`: a URL, or a path relative to the item folder (`/` = repo root).

Sources are written as `owner/repo`, a GitHub URL to a folder, a GitHub URL
to a `.json` file, or any https URL to a catalog file.

## Configuration

Environment variables (defaults in brackets):

| Variable | |
|---|---|
| `CASAMOD_STORE_ADDR` [`:8418`] | listen address |
| `CASAMOD_STORE_DATA` [`/DATA/AppData/casamod-store`] | settings and state |
| `CASAMOD_DIR` [`/DATA/AppData/casamod`] | CasaMOD folder |
| `DOCKER_SOCKET` [`/var/run/docker.sock`] | Docker socket |
| `CASAMOD_STORE_RESTART_DELAY` [`3`] | seconds to wait (and coalesce) before restarting CasaMOD |

Everything else (catalogs, interval, container name, notifier, token) is in
the Settings tab.

## Development

```sh
go test ./...                 # unit and integration tests (fake GitHub, fake Docker)
node tests/run.mjs            # browser tests (needs Go and Chrome/Chromium; set CHROME_BIN if needed)
SCREENSHOTS=out node tests/run.mjs
go run ./cmd/casamod-store    # with CASAMOD_STORE_DATA / CASAMOD_DIR pointing to temp folders
```

- `cmd/casamod-store`: entry point.
- `internal/github`: GitHub API/codeload client and URL parsing.
- `internal/installer`: safe tarball extraction and atomic swaps.
- `internal/store`: settings, `installed.json`, catalogs.
- `internal/app`: install/update/remove, update checks, HTTP API.
- `internal/docker`: restarts CasaMOD over the Docker socket.
- `internal/notifier`: the embedded `casamod-store-notifier` mini-mod.
- `web/static`: the web UI (plain HTML/CSS/JS, embedded in the binary).
- `tests/`: browser tests: a dependency-free Chrome DevTools harness, a fake
  GitHub server and a mock CasaOS page that loads the notifier.

The image is published to `ghcr.io/nescauquente/casamod-store` by
`.github/workflows/docker-publish.yml` on pushes to `main` and on `v*` tags.
