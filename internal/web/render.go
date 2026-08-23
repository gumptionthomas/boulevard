package web

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/version"
)

// render executes a page template into a buffer first, and only writes the
// status line and body once that succeeds. Templates are parsed at
// construction, so the only failure left here is a bad field reference,
// which is a bug rather than a condition to recover from — but writing
// status before execution would mean a failure ships the original status
// with a truncated body, since the header is already committed by the time
// ExecuteTemplate can fail. Buffering first keeps the promise of a clean
// 500 on failure true.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	// The AGPL footer is stamped here rather than by each handler. DESIGN.md
	// §2 asks for compliance by construction — a host who never thinks about
	// licensing should be compliant anyway — and a handler that forgets a
	// field is exactly the thinking it is meant to remove. Only about.html
	// carried the link while eight pages existed.
	switch d := data.(type) {
	case pageData:
		d.SourceURL = version.RepoURL
		d.BuildLine = "boulevard " + version.Version + " (" + version.Commit + ")"
		data = d
	case stewardData:
		d.SourceURL = version.RepoURL
		d.BuildLine = "boulevard " + version.Version + " (" + version.Commit + ")"
		data = d
	}

	tmpl, ok := s.tmpl[name]
	if !ok {
		log.Printf("render: no template registered for %q", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	noStore(w)
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck // best-effort write to an already-committed response
}

// noStore keeps a cookie-dependent page out of every cache.
//
// The shelf's body differs entirely depending on bl_session: with one it
// carries "You're at the shelf" and a deadline, without one it carries the
// scan-the-code explanation. This branch ships no TLS, so a steward putting
// the box on the internet is pushed toward a reverse proxy or CDN — where a
// shared cache holding either version and handing it to the wrong visitor is
// the failure. `private` plus `Vary: Cookie` would be the narrower choice,
// but it leaves correctness resting on every intermediary implementing Vary
// properly, and buys nothing worth having: these pages are a few kilobytes
// of inlined HTML with no items on them yet. `Vary: Cookie` is sent anyway,
// so a cache that ignores no-store still has the varying header it needs.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Cookie")
}

// humanDeadline renders a wall-clock deadline the way a person would say it.
//
// "4:12 PM tomorrow" beats "24 hours" because it answers the question the
// reader actually has: do I write the note now, or at the kitchen table?
// It renders in the server's local timezone — see spec §10.2 for why the
// alternatives are worse.
//
// `expires` arrives in UTC, because the store writes RFC3339 in UTC and
// time.Parse hands the value back in UTC. Both Format and the calendar
// fields below read the *value's own* location, so formatting it as it
// arrives names a UTC clock time and a UTC calendar day while `now` is
// local — which pushed an evening scan in a western zone a whole day out
// and dropped it into the date fallback. Converting into now's location
// first is the fix, and it belongs here at the presentation boundary: the
// stored instant is correct, only its rendering was not.
//
// The day comparison is on calendar dates rather than YearDay, so that a
// deadline crossing 31 December still reads "tomorrow".
func humanDeadline(expires, now time.Time) string {
	local := expires.In(now.Location())
	clock := local.Format("3:04 PM")

	ly, lm, ld := local.Date()
	ny, nm, nd := now.Date()
	ty, tm, td := now.AddDate(0, 0, 1).Date()

	switch {
	case ly == ny && lm == nm && ld == nd:
		return clock + " today"
	case ly == ty && lm == tm && ld == td:
		return clock + " tomorrow"
	default:
		return fmt.Sprintf("%s on %d %s", clock, ld, lm)
	}
}

// humanDate renders "7 September" for the card pages, adding the year when
// the date falls in a different calendar year than today.
//
// The year is not decoration in that case: a booklet's twelfth card is
// nearly a year out, and "7 August" on a card that starts working in 2027
// reads as a date that has already passed.
func humanDate(d, today boulevard.Date) string {
	if d.Year != today.Year {
		return fmt.Sprintf("%d %s %d", d.Day, d.Month, d.Year)
	}
	return fmt.Sprintf("%d %s", d.Day, d.Month)
}
