package web

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
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
//
// Nor is a remote address logged. This line already carries item ids
// (they're in the path); add a session id or an IP next to one and a take's
// log line becomes exactly the durable "who took what" record
// session_takes is engineered not to be (CLAUDE.md's invariants). Do not
// add either for debugging — that is the natural-sounding change that would
// rebuild it silently.
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

// redactPath removes the secret from a scan URL, and defuses everything it
// does log. Everything else on this server is a public path with nothing in
// it worth hiding — but "harmless" is not the same as "inert".
//
// r.URL.Path is percent-decoded, so a slug a stranger can put in a link
// carries any byte they like, newline included. Logging it raw lets
// /b/x%0A2026-08-15%20GET%20/%20200/leave write a second, forged line into
// the file a steward reads when the box "doesn't work" — and the same
// escape sequences that can redraw the approval queue can redraw a log
// tailed in a terminal. Sanitize is the same helper the queue prints
// through, for the same reason.
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
	return boulevard.Sanitize(p)
}
