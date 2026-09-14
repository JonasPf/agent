package app

import (
	"net/http"
	"os"
	"strings"
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
		out["changelog"] = changelogEntries(string(b))
	}
	writeJSON(w, 200, out)
}

// changelogEntries is the part of the file the version screen is served: from
// its first dated section on. What sits above that is a note to whoever writes
// the file — how it is organised, and that a change to behaviour belongs in it
// in the same commit — which is a rule for working in this repository and is
// kept in CLAUDE.md. This screen is read on a phone, once, after an upgrade, by
// someone who wants to know what changed; instructions to its authors are not
// that. A file with no section has no entries and returns empty, which the
// interface already reports as a changelog it cannot show.
func changelogEntries(s string) string {
	if strings.HasPrefix(s, "## ") {
		return s
	}
	if i := strings.Index(s, "\n## "); i >= 0 {
		return s[i+1:]
	}
	return ""
}
