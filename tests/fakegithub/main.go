// Command fakegithub serves the in-memory GitHub used by the browser tests.
// Usage: fakegithub -addr 127.0.0.1:0 -ready ready.txt (writes the URL).
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/nescauquente/casamod-store/internal/fakegithub"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	ready := flag.String("ready", "", "file that receives the base URL once listening")
	flag.Parse()

	gh := fakegithub.New()
	gh.Push("me/catalog", "main", "catalog", map[string]string{
		"store.json": `{
  "name": "Test catalog",
  "items": [
    {"id": "live-wallpapers", "name": "Live Wallpapers", "type": "mod", "repo": "me/mods", "path": "casaos-live-wallpapers/live-wallpapers", "description": "Animated wallpapers for CasaOS"},
    {"id": "music-player", "name": "Music Player", "type": "mod", "repo": "me/mods", "path": "casaos-music-player/music-player-widget", "description": "A music widget"},
    {"id": "star-wallpaper", "name": "Starry Orbit", "type": "wallpaper", "repo": "me/walls", "path": "star-wallpaper", "description": "Stars"},
    {"id": "plexus-wallpaper", "name": "Neural Plexus", "type": "wallpaper", "repo": "me/walls", "path": "plexus-wallpaper", "description": "Lines"}
  ]
}`,
	})
	gh.Push("me/mods", "main", "mods", map[string]string{
		"casaos-live-wallpapers/live-wallpapers/mod.js":   "console.log('live v1')",
		"casaos-music-player/music-player-widget/mod.js":  "console.log('music v1')",
		"casaos-music-player/music-player-widget/mod.css": ".x{}",
		"README.md": "mods",
	})
	gh.Push("me/walls", "main", "walls", map[string]string{
		"star-wallpaper/index.html":   "<html>star</html>",
		"star-wallpaper/project.json": "{}",
		"plexus-wallpaper/index.html": "<html>plexus</html>",
		"extra-wallpaper/index.html":  "<html>extra</html>",
	})
	gh.Push("Cp0204/CasaMOD", "main", "casamod", map[string]string{
		"app/mod/widget-sortable/mod.js": "x",
		"app/mod/show-dotfiles/mod.js":   "y",
		"app/run.sh":                     "z",
	})

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	if *ready != "" {
		os.WriteFile(*ready, []byte(url), 0o644)
	}
	log.Printf("fake GitHub on %s", url)
	log.Fatal(http.Serve(l, gh))
}
