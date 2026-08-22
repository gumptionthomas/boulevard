package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// maxStewardSettingsBody caps the settings form's body. Six short fields —
// the same reasoning as maxStewardLoginBody (steward.go), sized up because
// this form has more than one field but none of them approach note-sized
// text.
const maxStewardSettingsBody = 8 << 10

// handleStewardSettings renders the form, pre-filled with what is stored
// now.
//
// Unlike the other steward GET handlers, this one does not read a `notice`
// query parameter: a successful save re-renders in place instead of
// redirecting (see handleStewardSettingsSubmit), specifically so no message
// this handler shows ever has to arrive as free text in a URL — see that
// handler's doc comment for why.
func (s *Server) handleStewardSettings(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardSettings(w, lib, http.StatusOK, lib, nil, "", "")
}

// handleStewardSettingsSubmit is the read-modify-write this task exists for.
//
// UpdateLibrary overwrites nine columns from a whole boulevard.Library value
// — including Slug and BaseURL, neither of which this form exposes. Building
// one from form input alone would silently blank both: every /b/{slug}/ URL
// ever shared, and the base URL baked into twelve printed cards. So the save
// path loads the current row first (LibraryByID) and mutates only the six
// fields this form owns, leaving Slug, BaseURL and the unlisted
// StewardContact exactly as they were. The steward key hash cannot be
// touched this way even in principle — it is deliberately not a field on
// boulevard.Library — but a settings save must still leave the steward able
// to log in, which is why the tests assert that directly rather than
// trusting the type system alone.
//
// A rename never moves the slug (DESIGN.md §10: the slug is what
// single-library mode's root redirect targets, and changing it needs a
// redirect map v2 does not have yet) — which is exactly what loading the
// current row and never touching lib.Slug guarantees.
//
// On success this re-renders the settings page directly (200) rather than
// redirecting through a `?notice=`-style query parameter. That pattern —
// introduced elsewhere for other steward mutations — puts free-form prose
// inside a URL on an authenticated admin surface: html/template escapes it,
// so it is not script injection, but escaping does not stop it from reading
// as trustworthy text rendered at the app's own genuine URL, in the app's
// own voice, which is exactly what a phished "re-enter your steward key"
// message would want. A settings save has no such risk to buy: it is
// idempotent (saving the same values twice does nothing surprising), so
// there is no double-submit hazard a 303 would be protecting against, and
// the notice text here is server-computed from a live count, never user
// input — nothing about it needs to survive a redirect at all.
func (s *Server) handleStewardSettingsSubmit(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStewardSettingsBody)
	if err := r.ParseForm(); err != nil {
		noStore(w)
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	location := strings.TrimSpace(r.PostFormValue("location"))
	approval := r.PostFormValue("approval") != ""

	errs := map[string]string{}
	if name == "" {
		errs["name"] = "The name can't be blank."
	}
	if location == "" {
		errs["location"] = "The location can't be blank."
	}

	slots, slotsOK := parseSetting(r.PostFormValue("slots"), 1, errs, "slots",
		"Slots must be a whole number, at least 1.")
	maxAge, maxAgeOK := parseSetting(r.PostFormValue("max_age"), 0, errs, "max_age",
		"Max age must be a whole number, zero or more. Zero means never expire.")
	copies, copiesOK := parseSetting(r.PostFormValue("copies"), 0, errs, "copies",
		"Copies must be a whole number, zero or more.")

	if len(errs) > 0 {
		// The rejected submission comes back so the steward sees what they
		// typed, not what is still stored — but only for the fields that
		// parsed; a field that failed to parse falls back to what was
		// stored, since there is no valid int to echo. change nothing means
		// nothing is written either way.
		display := lib
		display.Name = name
		display.LocationLabel = location
		display.ApprovalRequired = approval
		if slotsOK {
			display.Slots = slots
		}
		if maxAgeOK {
			display.MaxAgeDays = maxAge
		}
		if copiesOK {
			display.DefaultCopies = copies
		}
		s.renderStewardSettings(w, lib, http.StatusUnprocessableEntity, display, errs, "", "")
		return
	}

	// The read: load the row fresh rather than trust `lib`, which
	// requireSteward resolved at the top of this request and which already
	// carries every field this form does not own.
	current, err := s.store.LibraryByID(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// Read before the write so the shelved count in the notice below
	// reflects what was true when the steward pressed save, not some racing
	// approval. Nothing here evicts anything: DESIGN.md's FIFO eviction
	// fires only from ApproveItem, so a shelf that is now "over slots"
	// simply sits there until the next approval drains it by one.
	shelved, err := s.store.ShelvedItems(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// The modify: only the six fields this form owns. Slug, BaseURL and
	// StewardContact are left exactly as LibraryByID returned them.
	current.Name = name
	current.LocationLabel = location
	current.Slots = slots
	current.MaxAgeDays = maxAge
	current.DefaultCopies = copies
	current.ApprovalRequired = approval

	// The write.
	if err := s.store.UpdateLibrary(r.Context(), current); err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	notice := "Saved."
	if slots < len(shelved) {
		notice = fmt.Sprintf(
			"Saved. The shelf holds %d items and you are setting %d slots. Nothing is removed now; the oldest will move to the shed as new items are approved.",
			len(shelved), slots)
	}

	// current now holds what was just written, so it doubles as both the
	// header's LibraryName source and the form's redisplayed values.
	s.renderStewardSettings(w, current, http.StatusOK, current, nil, notice, "")
}

// parseSetting reads one of the three numeric fields, records a field error
// under `field` if it fails to parse or falls below `min`, and reports
// whether the parse itself succeeded — separately from whether the value was
// in range — so a re-render can still echo a syntactically valid but
// out-of-range number (0 slots, -1 copies) rather than falling back to
// whatever was last stored.
func parseSetting(raw string, min int, errs map[string]string, field, msg string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		errs[field] = msg
		return 0, false
	}
	if n < min {
		errs[field] = msg
	}
	return n, true
}

// renderStewardSettings draws the form. `form` supplies the six field
// values shown in the inputs — the persisted library on a plain GET, or a
// display-only value carrying what the steward just typed on a rejected
// submission — while LibraryName in the page header always comes from
// `lib`, the actually-stored row, since a rejected rename must not relabel
// the page it failed on.
func (s *Server) renderStewardSettings(w http.ResponseWriter, lib boulevard.Library, status int,
	form boulevard.Library, errs map[string]string, notice, errMsg string) {
	s.render(w, status, "steward-settings.html", stewardData{
		Title:       "Settings — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     form,
		Errors:      errs,
		Notice:      notice,
		Error:       errMsg,
	})
}
