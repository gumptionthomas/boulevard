package web

import (
	"io"
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
	mux.HandleFunc("GET /b/{slug}/steward/queue", s.handleStewardQueue)
	mux.HandleFunc("GET /b/{slug}/steward/shelf", s.handleStewardShelf)
	mux.HandleFunc("GET /b/{slug}/steward/shed", s.handleStewardShed)
	mux.HandleFunc("GET /b/{slug}/steward/tokens", s.handleStewardTokens)
	mux.HandleFunc("GET /b/{slug}/steward/settings", s.handleStewardSettings)
	mux.HandleFunc("POST /b/{slug}/steward/settings", s.handleStewardSettingsSubmit)
	// POST only, like every steward mutation (and take before it): a GET
	// mutation is shareable, prefetchable by a browser or link scanner, and
	// triggerable by anything that renders a URL.
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/approve", s.handleStewardApprove)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/reject", s.handleStewardReject)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/remove", s.handleStewardRemove)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/pin", s.handleStewardPin)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/unpin", s.handleStewardUnpin)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/reshelve", s.handleStewardReshelve)
	mux.HandleFunc("POST /b/{slug}/steward/i/{id}/release", s.handleStewardRelease)
	return logRequests(hideUnmatchedMethods(mux))
}

// hideUnmatchedMethods turns net/http's 405 into a plain 404.
//
// Method matching happens in the mux, before any handler runs. Every steward
// mutation is POST-only, so a GET at /b/{slug}/steward/i/{id}/approve never
// reaches requireSteward — and requireSteward is the only thing that
// enforces "404 until a key exists" (DESIGN.md §6). The guard sat behind the
// door it was meant to guard: on a box with no steward key, that GET
// returned 405 with an Allow header naming the method it wanted, confirming
// the route was real. The slug did not even have to exist, so the whole
// admin route shape was enumerable against any host before a single key had
// been minted. A fresh install must not advertise a surface that is not
// armed, and a 405 advertises more than the 403 that rule already forbids.
//
// This is deliberately blunt: it covers the public POST-only routes (take,
// untake) too, where the disclosure is milder but the reasoning is the same.
// Nothing here wants to tell a stranger that a path exists but wants a
// different verb.
func hideUnmatchedMethods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&methodHider{ResponseWriter: w}, r)
	})
}

// methodHider rewrites one status and swallows the body that followed it.
// The Allow header is the actual disclosure, so it goes with them.
type methodHider struct {
	http.ResponseWriter
	hidden bool
}

func (m *methodHider) WriteHeader(status int) {
	if status == http.StatusMethodNotAllowed {
		m.hidden = true
		m.Header().Del("Allow")
		m.ResponseWriter.WriteHeader(http.StatusNotFound)
		// Byte-for-byte what http.NotFound writes, so a hidden 405 is
		// indistinguishable from a path that never existed at all.
		io.WriteString(m.ResponseWriter, "404 page not found\n")
		return
	}
	m.ResponseWriter.WriteHeader(status)
}

func (m *methodHider) Write(b []byte) (int, error) {
	if m.hidden {
		return len(b), nil
	}
	return m.ResponseWriter.Write(b)
}
