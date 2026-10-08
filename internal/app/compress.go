package app

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// compressed gzips what the agent sends to a browser that accepts it. A
// transcript is JSON carrying every tool result, read on a phone, and JSON and
// the interface's scripts shrink five to ten times; nothing in front of the
// agent can be relied on to do it.
//
// Three things go out as they are: the socket, which is not a response body; a
// file from a session's directory, which is often compressed already and whose
// byte ranges have to mean the file's own bytes; and an exported archive, which
// is a zip.
func compressed(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || !acceptsGzip(r) || r.URL.Path == "/ws" ||
			strings.Contains(r.URL.Path, "/files/") || strings.HasSuffix(r.URL.Path, "/export") {
			h.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		h.ServeHTTP(gw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(name) == "gzip" && strings.ReplaceAll(params, " ", "") != "q=0" {
			return true
		}
	}
	return false
}

// compressible names the bodies worth compressing: text, by what the handler
// said it was sending.
func compressible(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	ct = strings.TrimSpace(strings.ToLower(ct))
	return strings.HasPrefix(ct, "text/") || ct == "application/json" ||
		ct == "application/javascript" || ct == "application/manifest+json" ||
		ct == "image/svg+xml"
}

// Speed over the last few percent: a transcript of several megabytes is
// compressed while the operator waits for it.
var gzipPool = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return zw
}}

// gzipWriter decides at the status line whether to compress: only a complete
// answer (200) of a text type, not already encoded.
type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	decided bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		hdr := g.Header()
		if code == http.StatusOK && hdr.Get("Content-Encoding") == "" && compressible(hdr.Get("Content-Type")) {
			hdr.Del("Content-Length")
			hdr.Set("Content-Encoding", "gzip")
			hdr.Add("Vary", "Accept-Encoding")
			g.zw = gzipPool.Get().(*gzip.Writer)
			g.zw.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) close() {
	if g.zw == nil {
		return
	}
	_ = g.zw.Close()
	g.zw.Reset(nil)
	gzipPool.Put(g.zw)
	g.zw = nil
}
