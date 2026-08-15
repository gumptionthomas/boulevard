package web

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// render executes a page template. Templates are parsed at construction, so
// the only failure left here is a bad field reference, which is a bug rather
// than a condition to recover from — log it and show a bare 500.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
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
