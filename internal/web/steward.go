package web

import (
	"crypto/rand"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// stewardCookieName is the steward's own cookie, distinct from cookieName
// (bl_session) so the two grants — a stranger's 24-hour presence and a
// steward's 30-day admin session — can never be confused for one another.
const stewardCookieName = "bl_steward"

// maxStewardLoginBody caps the one route on this server a stranger can POST
// to without any credential at all: the login form. The same reasoning as
// maxLeaveBody (leave.go) applies at a smaller size, since a login body is
// one field.
const maxStewardLoginBody = 4 << 10

// stewardData is the admin's own view model. It is deliberately not
// pageData: the public pages carry items, forms and take-control state that
// mean nothing here, and the admin carries counts and settings that mean
// nothing there. One struct for both would be a grab bag neither page could
// be read against.
type stewardData struct {
	Title       string
	LibraryName string
	ShelfURL    string
	StewardURL  string
	SourceURL   string
	BuildLine   string

	Notice string
	Error  string

	Waiting int
	Shelved int
	Slots   int
	Pinned  int
	Shed    int
	ShedWhy map[string]int

	Items   []boulevard.Item
	Library boulevard.Library
	Errors  map[string]string

	// Now is s.now(), carried onto the page so a queue/shelf/shed row can
	// convert a stored-UTC timestamp into the display zone with
	// `.In $.Now.Location` — never time.Local, so a test on an injected
	// non-UTC clock renders in that clock's zone rather than the process's
	// own (CLAUDE.md: "clocks are injected, never read in place").
	Now time.Time
}

func stewardPath(lib boulevard.Library) string { return shelfURL(lib) + "steward/" }

// stewardKeyed resolves the library and refuses (404) if no steward key is
// set. It is the first two of requireSteward's four ordered steps, split out
// so the login handlers can share them without going through requireSteward
// itself — which would redirect the login form to itself.
//
// Not 403, and not a "set up your key" page: a fresh, unarmed install should
// not advertise a steward surface at all. This 404 does not, and does not
// need to, conceal whether the slug names a real library — /b/{slug}/
// already renders a shelf to anyone who asks, so slug existence is public
// regardless of what this route says.
func (s *Server) stewardKeyed(w http.ResponseWriter, r *http.Request) (boulevard.Library, bool) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return boulevard.Library{}, false
	}
	set, err := s.store.StewardKeyIsSet(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return boulevard.Library{}, false
	}
	if !set {
		// F9: this 404 was uncached, so an intermediary could serve the
		// unarmed-install response across a `steward-key` run that had
		// since armed the box, making a freshly working admin surface look
		// broken. Left as the stdlib's plain 404 rather than invalid.html
		// on purpose: this branch's whole point is to advertise nothing
		// about a steward surface existing at all.
		noStore(w)
		http.NotFound(w, r)
		return boulevard.Library{}, false
	}
	return lib, true
}

// requireSteward resolves the library, refuses if no key is set, and
// requires a live steward session. Every steward handler starts with it.
//
// The order matters. "No key set" 404s before anything else is considered,
// so an unarmed install advertises no steward surface at all — see
// stewardKeyed for why that 404 need not (and does not) also hide slug
// existence.
func (s *Server) requireSteward(w http.ResponseWriter, r *http.Request) (boulevard.Library, boulevard.StewardSession, bool) {
	lib, ok := s.stewardKeyed(w, r)
	if !ok {
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}

	// Housekeeping on the read path, the pattern Milestone 3 established:
	// no goroutine, no timer, and a failure is logged rather than fatal.
	if _, err := s.store.SweepExpiredStewardSessions(r.Context(), s.now()); err != nil {
		log.Printf("steward sessions not swept: %v", err)
	}

	// Final review, F5: SweepExpiredItems ran on the public shelf and item
	// handlers but nowhere a steward looked, so a steward page could show a
	// shelved item that had aged past max_age_days on a box nobody had
	// loaded the public shelf on recently. renderStewardShelf's job is to be
	// the authoritative view — on a quiet box (DESIGN.md's typical case)
	// with live Pin and Remove controls sitting on items that had actually
	// already expired, and a press of Remove there filed the item as
	// `removed` rather than `expired`, destroying the distinction §6 added
	// the fourth reason to preserve.
	if _, err := s.store.SweepExpiredItems(r.Context(), lib.ID, s.now()); err != nil {
		log.Printf("items not swept: %v", err)
	}

	sess, live := s.stewardSession(w, r, lib)
	if !live {
		// This redirect branches on the cookie, so Vary: Cookie belongs on
		// it as much as on any rendered page — noStore sets that alongside
		// Cache-Control: no-store.
		noStore(w)
		http.Redirect(w, r, stewardPath(lib)+"login", http.StatusSeeOther)
		return boulevard.Library{}, boulevard.StewardSession{}, false
	}
	return lib, sess, true
}

// stewardSession reads the cookie, looks the row up with s.now(), and checks
// it belongs to this library. It renews only when less than half the window
// remains — a steward loading five admin pages must not cost five writes
// through the one connection SetMaxOpenConns(1) allows.
//
// RenewStewardSession's write is conditional on the row not having already
// expired. This call site loads through StewardSessionByID first, which
// already refuses an expired row, so the guard is belt and braces here — but
// with it in place, ErrNotFound from the renewal means either "no such id"
// or "already expired". Logging and continuing is correct for both, because
// the session was already loaded and checked: a renewal failure here is
// housekeeping, not an auth decision.
//
// Final review, F4: renewal used to touch only the database row. The
// browser's own copy of expires_at is the cookie's MaxAge, set once at
// login to thirty days and never refreshed — so without re-issuing the
// cookie here, the browser dropped bl_steward exactly thirty days after
// login regardless of activity, while the row kept extending underneath
// it. Spec §3's stated purpose for renewal, "an active steward is not
// logged out mid-month," did not hold: an active steward was logged out on
// day thirty. `w` is threaded in so a successful renewal can call
// http.SetCookie alongside the database write. The cookie is only
// re-issued when the write actually succeeds — on a renewal failure the
// already-loaded `sess` (with its original, still-valid expiry) is
// returned unchanged, so the cookie the browser holds never claims an
// expiry the stored row does not also have.
func (s *Server) stewardSession(w http.ResponseWriter, r *http.Request, lib boulevard.Library) (boulevard.StewardSession, bool) {
	c, err := r.Cookie(stewardCookieName)
	if err != nil {
		return boulevard.StewardSession{}, false
	}
	now := s.now()
	sess, err := s.store.StewardSessionByID(r.Context(), c.Value, now)
	if err != nil || sess.LibraryID != lib.ID {
		return boulevard.StewardSession{}, false
	}

	if sess.ExpiresAt.Sub(now) < boulevard.StewardSessionTTL/2 {
		expiresAt := now.Add(boulevard.StewardSessionTTL)
		if err := s.store.RenewStewardSession(r.Context(), sess.ID, now, expiresAt); err != nil {
			log.Printf("steward session not renewed: %v", err)
		} else {
			sess.ExpiresAt = expiresAt
			http.SetCookie(w, stewardCookie(lib, sess.ID, boulevard.StewardSessionTTL))
		}
	}
	return sess, true
}

// handleStewardLogin renders the key form. It does not call requireSteward
// — that would redirect the login form to itself — but it still 404s when
// no key is set, via stewardKeyed.
func (s *Server) handleStewardLogin(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.stewardKeyed(w, r)
	if !ok {
		return
	}
	s.renderStewardLogin(w, lib, http.StatusOK, "")
}

// handleStewardLoginSubmit checks the key and either mints a session or
// refuses.
//
// A failed attempt sets no cookie, echoes nothing the caller submitted, and
// logs that an attempt happened without logging the submitted value —
// DESIGN.md's constraint on token secrets never reaching a log line applies
// here too, since the key is this admin's only credential. No lockout: a
// lockout on a single-credential system is a denial of service anyone can
// trigger, and there is nothing to brute-force against 128 bits.
func (s *Server) handleStewardLoginSubmit(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.stewardKeyed(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStewardLoginBody)
	if err := r.ParseForm(); err != nil {
		noStore(w)
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	key := r.PostFormValue("key")

	verified, err := s.store.VerifyStewardKey(r.Context(), lib.ID, key)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	if !verified {
		log.Printf("steward login failed for %q", lib.Slug)
		s.renderStewardLogin(w, lib, http.StatusUnauthorized, "Wrong key.")
		return
	}

	id, err := boulevard.NewStewardSessionID(rand.Reader)
	if err != nil {
		noStore(w)
		http.Error(w, "could not mint a session", http.StatusInternalServerError)
		return
	}
	now := s.now()
	sess := boulevard.StewardSession{
		ID:        id,
		LibraryID: lib.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(boulevard.StewardSessionTTL),
	}
	if err := s.store.CreateStewardSession(r.Context(), sess); err != nil {
		noStore(w)
		http.Error(w, "could not store the session", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.SetCookie(w, stewardCookie(lib, id, boulevard.StewardSessionTTL))
	http.Redirect(w, r, stewardPath(lib), http.StatusSeeOther)
}

// handleStewardLogout ends the session. POST-only, like every steward
// mutation: a GET logout would be a link anyone could plant.
func (s *Server) handleStewardLogout(w http.ResponseWriter, r *http.Request) {
	lib, sess, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteStewardSession(r.Context(), sess.ID); err != nil {
		log.Printf("steward session not deleted: %v", err)
	}
	noStore(w)
	http.SetCookie(w, expireStewardCookie(lib))
	http.Redirect(w, r, stewardPath(lib)+"login", http.StatusSeeOther)
}

// handleStewardHub answers the one question the hub exists for: is there
// anything for me? Most visits find nothing waiting — a steward checks far
// more often than they act — so the common path is the empty state, not
// the four-row summary; see steward-hub.html for why that state gets real
// design attention rather than reading as a blank page.
//
// The three list calls run one after another rather than nested. Each of
// PendingItems, ShelvedItems and ShedItems returns a slice and closes its
// own *sql.Rows before returning; SetMaxOpenConns(1) means a second query
// issued while the first's rows were still open would hang the one
// connection every request serializes through, not merely error.
func (s *Server) handleStewardHub(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}

	pending, err := s.store.PendingItems(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	shelved, err := s.store.ShelvedItems(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	shed, err := s.store.ShedItems(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	pinned := 0
	for _, it := range shelved {
		if it.Pinned {
			pinned++
		}
	}
	// Keyed by the human label (Label()), not the raw ShedReason, since the
	// template renders this straight into the page and the raw values
	// ("evicted", "taken") are the CLI/store vocabulary, not shelf copy.
	shedWhy := map[string]int{}
	for _, it := range shed {
		shedWhy[it.ShedReason.Label()]++
	}

	s.render(w, http.StatusOK, "steward-hub.html", stewardData{
		Title:       "Steward — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Waiting:     len(pending),
		Shelved:     len(shelved),
		Slots:       lib.Slots,
		Pinned:      pinned,
		Shed:        len(shed),
		ShedWhy:     shedWhy,
	})
}

func (s *Server) renderStewardLogin(w http.ResponseWriter, lib boulevard.Library, status int, errMsg string) {
	s.render(w, status, "steward-login.html", stewardData{
		Title:       "Steward login — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Error:       errMsg,
	})
}

// stewardCookie builds the Set-Cookie for a fresh session. HttpOnly and
// SameSite=Strict unconditionally — every steward route mutates, so none of
// them should be reachable from a link someone else wrote — and Secure
// whenever the library's own base URL is https, since serve terminates no
// TLS itself and cannot tell from the request alone.
func stewardCookie(lib boulevard.Library, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     stewardCookieName,
		Value:    value,
		Path:     stewardPath(lib),
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   strings.HasPrefix(lib.BaseURL, "https://"),
	}
}

// expireStewardCookie clears the cookie on logout, scoped identically to how
// it was set — a cookie cleared on a different Path is left in place by the
// browser rather than removed.
func expireStewardCookie(lib boulevard.Library) *http.Cookie {
	return &http.Cookie{
		Name:     stewardCookieName,
		Value:    "",
		Path:     stewardPath(lib),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   strings.HasPrefix(lib.BaseURL, "https://"),
	}
}
