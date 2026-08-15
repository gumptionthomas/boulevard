// Package web serves Boulevard's public surface. It owns HTTP and nothing
// else: validation stays pure in internal/tokens, persistence in
// internal/store.
package web

import (
	"embed"
	"html/template"
	"time"

	"github.com/gumptionthomas/boulevard/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

type Server struct {
	store *store.Store
	now   func() time.Time
	tmpl  *template.Template
}

// New builds a server. `now` is injected so handler tests can sit on an
// exact date — the grace-window boundaries depend on it.
//
// It panics if the embedded templates fail to parse: that is a build-time
// mistake, and failing at construction beats failing on a steward's first
// request.
func New(st *store.Store, now func() time.Time) *Server {
	return &Server{
		store: st,
		now:   now,
		tmpl:  template.Must(template.ParseFS(templateFS, "templates/*.html")),
	}
}
