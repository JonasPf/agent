package app

import (
	"net/http"
	"net/url"
	"strings"
)

// The agent has no login: a proxy in front of it asks for a password, and a
// browser that has been given the password sends it with every request to the
// agent's address — whichever page made the request. So a page on any other
// site the operator opens could act as them: start a conversation, send it a
// message, and have a shell run what it says; or open the socket and read
// every conversation as it is written. The browser's own rule stops such a
// page reading an answer, and nothing else.
//
// A browser says where a request came from, in headers a page cannot set. A
// request that changes something, or opens the socket, is refused when they
// say another site. A request that carries neither header is not from a
// browser — curl, a task, a test — and has no password to borrow.

// ownSite refuses what a page on another site sends.
func ownSite(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		if (!reads || r.URL.Path == "/ws") && fromAnotherSite(r) {
			fail(w, http.StatusForbidden, "this request came from a page on another site, and is refused")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// fromAnotherSite reports whether a browser sent this request for a page that
// is not the agent's own. Sec-Fetch-Site is the browser's own verdict. One
// that does not send it still sends Origin with anything that is not a plain
// read, and that is held against the host the request was sent to: the Host
// header, or what the proxy says the browser asked for when it puts its own
// name for the agent there.
func fromAnotherSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	return !strings.EqualFold(u.Host, r.Host) && !strings.EqualFold(u.Host, r.Header.Get("X-Forwarded-Host"))
}
