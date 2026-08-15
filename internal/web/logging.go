package web

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// logRequests writes one line per request: method, path, status, duration.
//
// A steward whose box "doesn't work" has nothing else to look at. One line
// per request is the difference between "it doesn't work" and "the scan
// 404s but the shelf loads".
//
// The path is redacted rather than logged as it arrived. A scan URL carries
// a card's secret, and a secret is the write credential for the shelf
// (DESIGN.md §4) — logging it raw would put a plaintext copy of the booklet
// in the one file a steward pastes into a support thread, and would keep it
// there after the card is revoked. Session ids are never logged at all:
// they live in a cookie, and nothing here reads headers.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, redactPath(r.URL.Path),
			rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// statusWriter remembers the status line. A handler that writes a body
// without calling WriteHeader has sent a 200, which is the zero state here.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// redactPath removes the secret from a scan URL. Everything else on this
// server is a public path with nothing in it worth hiding.
//
// The prefix test is case-insensitive on purpose, and it is not symmetry for
// its own sake. Go's ServeMux is case-sensitive, so a request to /S/<secret>
// never reaches the scan handler — it 404s. But it still carries a real
// secret, and a case-sensitive check here would write that secret to the log
// unredacted. The failure mode is mundane: someone types the URL from a card
// with caps lock on. A request that never matched a route is exactly the one
// whose path a steward is most likely to go looking at afterwards.
func redactPath(p string) string {
	if strings.HasPrefix(strings.ToLower(p), "/s/") {
		return "/s/<redacted>"
	}
	return p
}
