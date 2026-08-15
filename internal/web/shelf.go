package web

import (
	"errors"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// cookieName is the session cookie. Short, unmistakable, and namespaced so
// it cannot collide on a host serving several things.
const cookieName = "bl_session"

type pageData struct {
	Title       string
	LibraryName string
	Location    string
	// HasSession is the one predicate for presence. Deadline is display
	// only: two fields set together but branched on separately is how a
	// page ends up asking a different question than the one beside it.
	Deadline   string
	ShelfURL   string
	CardLabel  string
	StoppedOn  string
	StartsOn   string
	SourceURL  string
	BuildLine  string
	HasSession bool
	LeaveURL   string
	// AwaitingApproval is the confirmation page's one branch. Spec §6.3
	// forbids a confirmation that implies publication — but with approval
	// off, the item really is on the shelf already, and saying a steward
	// looks first would be the same lie in the other direction.
	AwaitingApproval bool
	Form             boulevard.Submission
	Errors           boulevard.FieldErrors
	ItemViews        []itemView
	Item             itemView
	// ItemBase is this library's item URL prefix ("/b/slug/i/"). The
	// templates build every item, take and untake link off it rather than
	// each calling itemURL itself.
	ItemBase string
	// MaxTakes is DESIGN.md §4's per-session limit, for the copy that
	// explains why the control went inert.
	MaxTakes int
	// Notice carries a refusal's explanation back onto the shelf — the take
	// and untake handlers always re-render the shelf on failure, whichever
	// page the control was pressed on (see refuseTake in take.go).
	Notice string
}

// itemView is one item plus what this viewer may do with it. The template
// must not work that out itself: the same shelf URL renders four different
// controls depending on the cookie, and a branch spread across two
// templates is how they drift apart.
type itemView struct {
	boulevard.Item
	// TakenByYou is set when this session already took a copy, which turns
	// the control into "Taken / Put it back".
	TakenByYou bool
	// AtLimit is set when this session has taken its three. The control
	// goes inert with an explanation rather than disappearing — show the
	// rule, do not hide the feature (§6).
	AtLimit bool
	// HasSession and ItemBase duplicate pageData fields onto the view
	// itself. They have to: the take-control markup is factored into a
	// shared `{{define "takecontrol"}}` block (shelf.html and item.html
	// both call it), and `{{template "name" pipeline}}` resets `$` to
	// pipeline inside the called template — a well-known text/template
	// trap, and a second one after Milestone 1's same-named-block
	// collision. `$.HasSession`/`$.ItemBase` would resolve against the
	// itemView, not the page's pageData, and fail. Carrying both directly
	// on the view sidesteps that instead of relying on a `$` that will not
	// point where a reader expects once it is inside a called template.
	HasSession bool
	ItemBase   string
}

// Takeable reports whether the control renders at all. A pinned item shows
// nothing: it has no copies and cannot be taken (§5), so there is no rule to
// explain — a pin is furniture, not stock.
func (v itemView) Takeable() bool { return !v.Pinned }

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
	http.Redirect(w, r, shelfPath(slugs[0]), http.StatusFound)
}

func (s *Server) handleShelf(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	s.renderShelf(w, r, lib, http.StatusOK, "")
}

// renderShelf loads the shelf and draws it. It is the one place that
// decides a take control's state, so both the shelf and (via handleItem) a
// single item's page read the same answer rather than each working it out.
//
// msg, when non-empty, is a refusal's explanation carried onto the shelf —
// handleTake and handleUntake re-render here on failure regardless of which
// page the control was on.
func (s *Server) renderShelf(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, msg string) {
	items, err := s.store.ShelvedItems(r.Context(), lib.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	data := pageData{
		Title:       lib.Name,
		LibraryName: lib.Name,
		Location:    lib.LocationLabel,
		LeaveURL:    leaveURL(lib),
		ShelfURL:    shelfURL(lib),
		ItemBase:    shelfURL(lib) + "i/",
		MaxTakes:    maxTakes,
		Notice:      msg,
	}

	sess, live := s.liveSession(r, lib.ID)
	if live {
		data.HasSession = true
		data.Deadline = humanDeadline(sess.ExpiresAt, s.now())
	}

	var taken map[string]bool
	if live {
		taken, err = s.store.TakenBySession(r.Context(), sess.ID)
		if err != nil {
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
	}
	atLimit := live && len(taken) >= maxTakes

	views := make([]itemView, len(items))
	for i, it := range items {
		views[i] = itemView{
			Item:       it,
			TakenByYou: taken[it.ID],
			AtLimit:    atLimit,
			HasSession: live,
			ItemBase:   data.ItemBase,
		}
	}
	data.ItemViews = views

	s.render(w, status, "shelf.html", data)
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
