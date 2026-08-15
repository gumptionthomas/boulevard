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
	tmpl  map[string]*template.Template
}

// pageTemplates are the page files that pair with layout.html to produce a
// full page. Each one is parsed into its own template set, independently of
// the others: every page file defines a block named "body", and parsing
// them all into one shared set (as ParseFS over templates/*.html would do)
// lets the last one parsed silently win, so every page renders whichever
// body happened to parse last. Parsing one page at a time keeps each page's
// "body" isolated to a set of exactly one.
var pageTemplates = []string{"shelf.html", "about.html", "outdated.html", "notyet.html", "invalid.html"}

// New builds a server. `now` is injected so handler tests can sit on an
// exact date — the grace-window boundaries depend on it.
//
// It panics if the embedded templates fail to parse: that is a build-time
// mistake, and failing at construction beats failing on a steward's first
// request.
func New(st *store.Store, now func() time.Time) *Server {
	tmpl := make(map[string]*template.Template, len(pageTemplates))
	for _, name := range pageTemplates {
		tmpl[name] = template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/"+name))
	}
	return &Server{
		store: st,
		now:   now,
		tmpl:  tmpl,
	}
}
