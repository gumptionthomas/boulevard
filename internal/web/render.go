package web

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
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
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck // best-effort write to an already-committed response
}

// humanDeadline renders a wall-clock deadline the way a person would say it.
//
// "4:12 PM tomorrow" beats "24 hours" because it answers the question the
// reader actually has: do I write the note now, or at the kitchen table?
// It renders in the server's local timezone — see spec §10.2 for why the
// alternatives are worse.
func humanDeadline(expires, now time.Time) string {
	clock := expires.Format("3:04 PM")
	nowDay := now.YearDay()
	sameYear := expires.Year() == now.Year()

	switch {
	case sameYear && expires.YearDay() == nowDay:
		return clock + " today"
	case sameYear && expires.YearDay() == nowDay+1:
		return clock + " tomorrow"
	default:
		return fmt.Sprintf("%s on %d %s", clock, expires.Day(), expires.Month())
	}
}

// humanDate renders "7 September" for the out-of-date page.
func humanDate(d boulevard.Date) string {
	return fmt.Sprintf("%d %s", d.Day, d.Month)
}
