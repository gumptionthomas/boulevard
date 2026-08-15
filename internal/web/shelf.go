package web

import (
	"errors"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/version"
)

// cookieName is the session cookie. Short, unmistakable, and namespaced so
// it cannot collide on a host serving several things.
const cookieName = "bl_session"

type pageData struct {
	Title       string
	LibraryName string
	Location    string
	Deadline    string // empty when there is no live session
	ShelfURL    string
	CardLabel   string
	StoppedOn   string
	StartsOn    string
	SourceURL   string
	BuildLine   string
	HasSession  bool
	LeaveURL    string
	Form        boulevard.Submission
	Errors      boulevard.FieldErrors
	Items       []boulevard.Item
	Item        boulevard.Item
}

// handleRoot redirects to the sole library, or 404s.
//
// Zero libraries has nothing to show. More than one is a 404 rather than a
// list, because a page enumerating every shelf on a host is the neighborhood
// map — a v2 surface with its own design (DESIGN.md §10). Each library stays
// reachable at its canonical /b/{slug}/ either way; only the convenience
// redirect is withheld.
//
// One query, not two: the slug list answers "how many are there" as well as
// "which one", so counting first was a second round trip and a window in
// which a library could appear between the count and the list.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	slugs, err := s.store.LibrarySlugs(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	if len(slugs) != 1 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/b/"+slugs[0]+"/", http.StatusFound)
}

func (s *Server) handleShelf(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	data := pageData{
		Title:       lib.Name,
		LibraryName: lib.Name,
		Location:    lib.LocationLabel,
	}
	if sess, live := s.liveSession(r, lib.ID); live {
		data.Deadline = humanDeadline(sess.ExpiresAt, s.now())
	}
	s.render(w, http.StatusOK, "shelf.html", data)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "about.html", pageData{
		Title:       "About " + lib.Name,
		LibraryName: lib.Name,
		Location:    lib.LocationLabel,
		SourceURL:   version.RepoURL,
		BuildLine:   "boulevard " + version.Version + " (" + version.Commit + ")",
	})
}

// libraryFromPath resolves {slug}. An unknown slug is a 404 — never a
// fallback to the sole library, which would defeat DESIGN.md §10's rule that
// nothing may assume a singleton.
//
// The 404 renders invalid.html rather than the stdlib's plain-text "404 page
// not found". Every other page here is built for one-handed outdoor reading,
// and this is the one a stale or mistyped URL actually reaches. invalid.html
// is safe to reuse: it names no library and links only to the host root, so
// it says nothing an unknown slug should not say.
func (s *Server) libraryFromPath(w http.ResponseWriter, r *http.Request) (boulevard.Library, bool) {
	lib, err := s.store.LibraryBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderInvalid(w)
		return boulevard.Library{}, false
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return boulevard.Library{}, false
	}
	return lib, true
}

// liveSession reports whether the request carries a session for THIS library.
// A session minted at one box grants nothing at another.
func (s *Server) liveSession(r *http.Request, id boulevard.LibraryID) (boulevard.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return boulevard.Session{}, false
	}
	sess, err := s.store.SessionByID(r.Context(), boulevard.SessionID(c.Value), s.now())
	if err != nil || sess.LibraryID != id {
		return boulevard.Session{}, false
	}
	return sess, true
}
