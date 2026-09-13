package app

import (
	"net/http"
	"os"
)

// Version is the commit the running build was made from, and BuiltAt is when it
// was made. Both are stamped in by the linker — `task build` from the working
// tree, the Dockerfile from the commit CI is releasing — because the image the
// agent runs in carries no repository to ask. The deployment tags images by
// commit, so this is the same string a rollback names.
//
// A build with no stamp says "dev". A number invented at runtime would name a
// build nobody can go back to, which is worse than saying nothing.
var (
	Version = "dev"
	BuiltAt = ""
)

// maxChangelog caps what is read from disk. The changelog is prose written by
// hand and shipped in the image; this is only so a file that is not one cannot
// be served whole.
const maxChangelog = 256 << 10

// hVersion answers with the build and the changelog that shipped beside it. The
// changelog is read on each request rather than at startup: it costs one small
// file read on a screen nobody opens twice, and it means a corrected file is the
// one served without restarting the agent.
func (a *App) hVersion(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{
		"version":   Version,
		"built_at":  BuiltAt,
		"changelog": "",
	}
	if b, err := os.ReadFile(a.cfg.ChangelogPath); err == nil {
		if len(b) > maxChangelog {
			b = b[:maxChangelog]
		}
		out["changelog"] = string(b)
	}
	writeJSON(w, 200, out)
}
