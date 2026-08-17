package web

import (
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// The canonical routes always carry the library (DESIGN.md §10). Four
// handlers were building them by hand in eight places, which is eight
// chances for one of them to drift from the mux patterns below — and the
// milestone that splits a library into its own database file is the one
// that will have to change them all at once.
func shelfPath(slug string) string          { return "/b/" + slug + "/" }
func shelfURL(lib boulevard.Library) string { return shelfPath(lib.Slug) }
func leaveURL(lib boulevard.Library) string { return shelfURL(lib) + "leave" }
func leftURL(lib boulevard.Library) string  { return shelfURL(lib) + "left" }

// itemURL is one item's page, or (with id == "") the base every item and
// take/untake URL is built from — the templates need that base rather than
// a per-item call, so it also lives on pageData as ItemBase.
func itemURL(lib boulevard.Library, id string) string { return shelfURL(lib) + "i/" + id }

// Handler wires the public surface.
//
// Two of these patterns are not design choices: Milestone 0 printed twelve
// cards encoding {base_url}/s/{secret} and a mounted sign encoding
// {base_url}. Those URLs exist on paper, and changing them invalidates a
// booklet.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /b/{slug}/{$}", s.handleShelf)
	mux.HandleFunc("GET /b/{slug}/i/{id}", s.handleItem)
	mux.HandleFunc("GET /b/{slug}/about", s.handleAbout)
	mux.HandleFunc("GET /b/{slug}/leave", s.handleLeaveForm)
	mux.HandleFunc("POST /b/{slug}/leave", s.handleLeaveSubmit)
	mux.HandleFunc("GET /b/{slug}/left", s.handleLeft)
	// POST only, deliberately: a GET take URL would be shareable,
	// prefetchable by a browser or link scanner, and — because the
	// response redirects to a submitted URL — an open redirect wearing the
	// shelf's domain. Do not add a GET form of either for convenience.
	mux.HandleFunc("POST /b/{slug}/i/{id}/take", s.handleTake)
	mux.HandleFunc("POST /b/{slug}/i/{id}/untake", s.handleUntake)
	mux.HandleFunc("GET /s/{token}", s.handleScan)

	mux.HandleFunc("GET /b/{slug}/steward/{$}", s.handleStewardHub)
	mux.HandleFunc("GET /b/{slug}/steward/login", s.handleStewardLogin)
	mux.HandleFunc("POST /b/{slug}/steward/login", s.handleStewardLoginSubmit)
	mux.HandleFunc("POST /b/{slug}/steward/logout", s.handleStewardLogout)
	return logRequests(mux)
}
