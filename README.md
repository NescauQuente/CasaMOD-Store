# CasaMOD Store

**English** | [Português (Brasil)](README.pt-BR.md)

A store for [CasaMOD](https://github.com/Cp0204/CasaMOD): install, update and
remove CasaMOD **mods** and **wallpapers** straight from GitHub, from a web UI
that opens from the app icon in CasaOS.

- **Store**: items from source repositories (by default
  [CasaMODs](https://github.com/UsuarioDeJava/CasaMODs) and
  [WE-Wallpapers](https://github.com/UsuarioDeJava/WE-Wallpapers)), the mods
  that ship with CasaMOD, or any GitHub repository/folder pasted as a URL.
- **Updates**: checked every 6 hours (configurable) or on demand. Per item you
  choose *Notify me* (default) or *Update automatically*.
- **Desktop notice**: a tiny mini-mod (`casamod-store-notifier`) shows a
  discreet notice on the CasaOS desktop when updates are waiting, with a link
  to the store. Texts in the 31 CasaOS languages.
- **Updates of the store itself**: shown in the store and in the desktop
  notice, applied with CasaOS' *Check then update*.
- Web UI in the 31 CasaOS languages.
- Small: one static Go binary (no dependencies), multi-arch image
  (amd64, arm/v7, arm64), a few MB of RAM.

## Requirements

- CasaOS 0.4.x.
- [CasaMOD](https://github.com/Cp0204/CasaMOD) installed and running (its
  container is named `casamod`; another name can be set in **Settings**).
- For wallpapers: the **Live Wallpapers** mod (from
  [CasaMODs](https://github.com/UsuarioDeJava/CasaMODs), also in the store),
  which plays the folders in `mod/wallpapers/`.

## Install on CasaOS

This repository is also a CasaOS app store source (`Apps/casamod-store/`).

1. Install CasaMOD first.
2. In CasaOS: **App Store → More apps**, add
   `https://github.com/UsuarioDeJava/CasaMOD-Store/archive/refs/heads/main.zip`.
3. Install **CasaMOD Store** from the App Store and open it from its icon
   (port `8418`).

On its first start the store installs the notice mini-mod and restarts
CasaMOD once; reload the CasaOS page afterwards.

CasaOS only offers *Check then update* for apps it finds in a source, so
installing from the source is what makes updates of the store work. A custom
install of
[`Apps/casamod-store/docker-compose.yml`](Apps/casamod-store/docker-compose.yml)
(**App Store → Custom Install → Import**) uses the same `store_app_id` and is
picked up as well once the source is added.

### Updating the store

When a new image is published the store lists **CasaMOD Store** in the
**Updates** tab and in the desktop notice. Update it in CasaOS: app icon menu
(⋮) → **Check then update**. Settings and installed items are kept: they live
in `/DATA/AppData/casamod-store`.

To have it done for you, set **CasaMOD Store** to *Update automatically*
(**Settings → Updates**, or the store's row in **Updates**). The store
cannot call CasaOS by itself (it has no CasaOS login), so the desktop notice
mini-mod does it: the next time CasaOS is open in a browser with someone
logged in, it makes the same call as *Check then update*
(`PATCH /v2/app_management/compose/casamod-store?force=true`), once per new
image. `force=true` also skips CasaOS' cached answer.

CasaOS caches the answer of that check for up to an hour. If it says the app
is up to date right after a release, wait, or run
`sudo systemctl restart casaos-app-management` on the server and check again.

### Uninstalling

1. In **Settings**, turn off **Notice on CasaOS** and save: the store removes
   its mini-mod and restarts CasaMOD. (Otherwise delete
   `/DATA/AppData/casamod/mod/casamod-store-notifier` and restart CasaMOD.)
2. Uninstall the app in CasaOS. Mods and wallpapers it installed stay in
   CasaMOD's folders; remove them in the store first if you do not want them.
3. Optionally delete `/DATA/AppData/casamod-store` (settings, token, list of
   installed items).

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

Without a token the GitHub API allows 60 requests per hour per IP. The store
uses one request per source when it loads the store (cached for 30 minutes)
and about one per installed item on each update check, which fits a few
dozen items. Add a token in **Settings** to install from private
repositories or to raise the limit to 5000/hour. A fine-grained token with
read-only *Contents* access to the repositories you need is enough. It is
stored only in `/DATA/AppData/casamod-store/config.json` (mode 0600) and is
never sent back to the browser.

## Using the store

The web UI is available in the 31 CasaOS languages and follows the browser's
language (English when it is not one of them); add `?lang=de`,
`?lang=pt-BR`, … to the address to pick one.

**Store**

- Search, filter by type (**Mods** / **Wallpapers**) or by source.
- **Install** installs one item. Tick several cards and use **Install
  selected** to install them in one go: CasaMOD is restarted once, at the end.
- **Add from GitHub URL**: paste a repository, folder or file URL, press
  **Read**, check the repository, branch, folder, id (folder name), type and
  update policy, then **Install**.
- Installing a wallpaper while the **Live Wallpapers** mod is missing asks
  first: **Install now** adds Live Wallpapers to the same install (one
  CasaMOD restart), **OK** installs only the wallpaper.
- **Refresh** reloads the sources now instead of waiting for the 30 minute
  cache.
- Warnings about sources that break the folder rules appear above the cards
  (see [Sources](#sources)).

**Installed**

- **Managed**: items installed by the store, with their source and commit.
  Change the update policy (*Notify me* / *Update automatically*), reinstall
  a missing item, or **Remove** it.
- **Manual**: folders in `mod/` and `mod/wallpapers/` that the store did not
  install. The store never changes or removes them; installing an item with
  the same name asks before replacing the folder.

**Updates**

- Items with a newer commit (and the store itself, when a new image exists).
- **Check now** checks right away; **Update all** updates every listed item.
- **Activity** shows what the store did (installs, updates, restarts, errors).

**Settings**

| Setting | |
|---|---|
| Token | GitHub token, see above. **Remove token** deletes it. |
| Sources | One per line, see [Sources](#sources). |
| CasaMOD mods | List the mods that ship with CasaMOD (from `Cp0204/CasaMOD`). |
| Check every (hours) | Interval of the automatic update check; `0` turns it off. |
| CasaMOD Store | Update policy of the store itself (see [Updating the store](#updating-the-store)). |
| Notice on CasaOS | Install the desktop notice mini-mod. |
| Store address | Link used by the notice, e.g. `http://casaos.local:8418`. Leave empty to use the CasaOS address with the store port. |
| Store port | Port used by the notice link when the address is empty. |
| Container | Name of the CasaMOD container to restart. |
| Restart CasaMOD | Restart it now. |

### Changing the port

Change the published port in the app settings in CasaOS, then set the same
value in **Settings → Store port** so the desktop notice links to the right
place.

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
- An item has an update when a newer commit touched **its folder**; commits
  elsewhere in the repository do not count.
- The store's own image records the commit it was built from; each check
  compares it with the last successful `docker-publish.yml` run on `main`.
- State lives in `/DATA/AppData/casamod-store`: `config.json` (settings and the
  optional GitHub token) and `installed.json` (source, commit and update
  policy of each item).

## Sources

A source is a GitHub repository (or a folder of one) added in **Settings →
Sources**, one per line, written as `owner/repo`, a GitHub URL to a folder, a
GitHub URL to a `.json` file, or any https URL to a catalog file.

Without a `store.json`, the store reads the repository tree (one API call)
and decides what each folder is by the files **directly inside it**:

| The folder has, at its root | It is a | Installed into |
|---|---|---|
| `mod.js` and/or `mod.css` | **mod** | `mod/<folder name>/` |
| `project.json` or `index.html` (and no `mod.js`/`mod.css`) | **wallpaper** | `mod/wallpapers/<folder name>/` |

Rules:

1. **The folder name is the id** (and the installed folder's name). Name it
   the way it should appear in `mod/` or `mod/wallpapers/`.
2. **Only the outermost match counts.** Once a folder is a mod or a
   wallpaper, everything inside it is part of that item: a mod folder that
   also contains a wallpaper folder hides that wallpaper.
3. **Mod wins.** A folder with `mod.js`/`mod.css` *and* `project.json` or
   `index.html` is a mod.
4. A page at the very root of the source (`index.html` next to the README)
   is not an item.
5. Hidden folders (`.github`, …) and `test`, `tests`, `testdata`,
   `fixtures`, `mock(s)`, `docs`, `doc`, `example(s)` and `node_modules`
   are skipped.
6. Name, description, version and preview come from `mod.json` (mods) or
   `project.json` (wallpapers) when present; otherwise the folder name is
   used.
7. **Card picture**: put `preview.png` (or `.jpg`, `.jpeg`, `.webp`,
   `.gif`) at the root of the item folder. A `preview` named in
   `mod.json`/`project.json` takes precedence. Pictures are loaded by the
   browser straight from GitHub, so they only show for public
   repositories.

Example layout:

```
my-mods/                      (source: owner/my-mods)
├── README.md
├── hide-footer/              → mod "hide-footer"
│   ├── mod.css
│   └── preview.png           (optional: picture of the card)
├── music-player-widget/      → mod "music-player-widget"
│   ├── mod.js
│   ├── mod.css
│   └── mod.json              (optional: name, description, version)
└── tests/                    → skipped

my-wallpapers/                (source: owner/my-wallpapers)
├── starry-orbit/             → wallpaper "starry-orbit"
│   ├── index.html
│   ├── project.json          (optional: title, description, preview)
│   └── js/ …
└── neural-plexus/            → wallpaper "neural-plexus"
    └── index.html
```

`mod.json` example:

```json
{ "name": "Music Player", "description": "A music widget", "version": "1.2.0", "author": "me" }
```

`project.json` is the Wallpaper Engine file; the store reads `title`,
`description` and `preview` (an image path relative to the folder).

The **Store** tab warns about a source when:

- a folder has both mod and wallpaper files (it is read as a mod);
- an item folder contains other item folders (they are ignored);
- two folders have the same name (they would install into the same
  folder, e.g. two `wallpaper-engine/` folders);
- the source has no mods or wallpapers at all.

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
  `mod.css` at its root, otherwise the install is refused.
- `id`: folder name; defaults to the last segment of `path`.
- `repo`: defaults to the source's repository; `branch`: defaults to the
  repository's default branch; `path`: defaults to the repository root.
- `preview`: a URL, or a path relative to the item folder (`/` = repo root).

## Troubleshooting

| Problem | What to do |
|---|---|
| A new mod does not show up in CasaOS | CasaMOD loads mods when it starts: wait for the restart (or **Settings → Restart CasaMOD**) and reload the CasaOS page. |
| "Docker socket not mounted" | The socket volume was removed: restart CasaMOD yourself after adding or removing mods. |
| "CasaMOD restart failed" | Check **Settings → Container** and use **Restart now**. |
| "Not found on GitHub" | Wrong repository, branch or folder, or a private repository without a token. |
| "GitHub API rate limit reached" | Wait an hour or add a token in **Settings**. |
| CasaOS says the store is up to date but the store shows a new version | The store was not installed from the app store source: add the source (see [Install](#install-on-casaos)). CasaOS also caches the answer for up to an hour. |
| The desktop notice opens the wrong address | Set **Settings → Store address** (or **Store port**). |
| A source shows a warning | Fix the folder layout in that repository (see [Sources](#sources)). |

## Configuration

Environment variables (defaults in brackets):

| Variable | |
|---|---|
| `CASAMOD_STORE_ADDR` [`:8418`] | listen address |
| `CASAMOD_STORE_DATA` [`/DATA/AppData/casamod-store`] | settings and state |
| `CASAMOD_DIR` [`/DATA/AppData/casamod`] | CasaMOD folder |
| `DOCKER_SOCKET` [`/var/run/docker.sock`] | Docker socket |
| `CASAMOD_STORE_RESTART_DELAY` [`3`] | seconds to wait (and coalesce) before restarting CasaMOD |

Everything else is in the Settings tab.

## Development

```sh
go test -race ./...           # unit and integration tests (fake GitHub, fake Docker)
node tests/run.mjs            # browser tests (needs Go and Chrome/Chromium; set CHROME_BIN if needed)
SCREENSHOTS=out node tests/run.mjs
go run ./cmd/casamod-store    # with CASAMOD_STORE_DATA / CASAMOD_DIR pointing to temp folders
```

Test hooks: `GITHUB_API_URL`, `GITHUB_CODELOAD_URL` and `GITHUB_RAW_URL`
point the store at a fake GitHub; `CASAMOD_STORE_COMMIT` sets the build
commit used by the self-update check.

- `cmd/casamod-store`: entry point.
- `internal/github`: GitHub API/codeload client and URL parsing.
- `internal/installer`: safe tarball extraction and atomic swaps.
- `internal/store`: settings, `installed.json`, sources (`catalog.go`,
  folder scan in `discover.go`).
- `internal/app`: install/update/remove, update checks, self-update check,
  HTTP API.
- `internal/docker`: restarts CasaMOD over the Docker socket.
- `internal/notifier`: the embedded `casamod-store-notifier` mini-mod.
- `internal/fakegithub`: in-memory GitHub used by the tests.
- `web/static`: the web UI (plain HTML/CSS/JS, embedded in the binary);
  texts in `web/static/lang/<code>.json`, one file per CasaOS language
  (`go test ./web/` checks that every file has every text).
- `tests/`: browser tests: a dependency-free Chrome DevTools harness, a fake
  GitHub server and a mock CasaOS page that loads the notifier.
- `Apps/casamod-store/`: the CasaOS app (app store source layout).

The image is published to `ghcr.io/usuariodejava/casamod-store` by
`.github/workflows/docker-publish.yml` on pushes to `main` that change the
program and on `v*` tags.
