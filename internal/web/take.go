package web

import (
	"errors"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// maxTakes is DESIGN.md §4's per-session take limit.
//
// Presence is attestable, not enforceable: someone standing at the box may
// scan again for a fresh session with fresh counters. That is not a
// loophole to close. Rotation plus rate limits bound the blast radius, and
// §4 says explicitly not to attempt to make this airtight.
const maxTakes = 3

// handleTake records a take and sends the visitor to the thing.
//
// One tap does both. With no JavaScript that means a 303 to the item's
// payload, which is already displayed and clickable on the shelf — so this
// exposes nothing a reader could not already reach. Nothing fetches it (§3);
// it goes into the Location header as stored.
func (s *Server) handleTake(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	itemID := r.PathValue("id")

	sess, live := s.liveSession(r, lib.ID)
	if !live {
		s.refuseTake(w, r, lib, http.StatusForbidden, "")
		return
	}

	// invalid.html, not the stdlib 404 (F8): the item may have been public a
	// minute ago and shed since, by expiry or by someone else's take, so a
	// shared link to it going stale is now routine rather than a bug. See
	// item.go's handleItem for the fuller reasoning; it applies here too.
	it, err := s.store.ItemByID(r.Context(), lib.ID, itemID)
	if errors.Is(err, store.ErrNotFound) {
		s.renderInvalid(w)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	err = s.store.TakeItem(r.Context(), lib.ID, sess.ID, itemID, s.now(), maxTakes)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// TakeItem's ErrNotFound covers "does not exist" and "no longer
		// takeable" alike, including the genuine race this friendly copy
		// exists for: another tap emptied this exact item between the
		// ItemByID above and here. TakeItem sheds an item the instant it
		// reaches zero, in the same transaction as the decrement (I-2), so
		// that race lands here rather than as ErrNoCopiesLeft — without
		// this check it fell through to a bare 404 for the one case the
		// friendly copy was written for. Re-reading the item is the only
		// way to tell that race apart from a truly unknown id, a pin, or an
		// item shed for some other reason meanwhile; all of those still
		// 404.
		if now, nerr := s.store.ItemByID(r.Context(), lib.ID, itemID); nerr == nil &&
			now.State == boulevard.ItemShed && now.ShedReason == boulevard.ShedTaken {
			s.refuseTake(w, r, lib, http.StatusConflict, "Someone took the last one.")
			return
		}
		s.renderInvalid(w)
		return
	case errors.Is(err, store.ErrLimitReached):
		s.refuseTake(w, r, lib, http.StatusForbidden,
			"You've taken three things with this scan. Scan the card again for more.")
		return
	case errors.Is(err, store.ErrNoCopiesLeft):
		// Reachable only when default_copies is 0, where the item was never
		// takeable in the first place — "someone took the last one" would
		// be untrue there, so this copy has to hold for that case instead
		// (I-2).
		s.refuseTake(w, r, lib, http.StatusConflict,
			"There are no copies of this to take.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// 303 so a reload cannot take the same thing twice, and — matching
	// handleLeaveSubmit's own redirect — noStore so nothing caches a
	// response whose Location depends on this cookie's session state.
	noStore(w)
	http.Redirect(w, r, takeDestination(lib, it), http.StatusSeeOther)
}

// takeDestination is where a successful take sends you. A text item has
// nothing to open, so it goes back to the shelf anchored at itself.
func takeDestination(lib boulevard.Library, it boulevard.Item) string {
	if it.Type == boulevard.ItemText {
		return shelfURL(lib) + "#i-" + it.ID
	}
	return it.Payload
}

// handleUntake reverses a take, and always returns to the shelf.
func (s *Server) handleUntake(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	itemID := r.PathValue("id")

	sess, live := s.liveSession(r, lib.ID)
	if !live {
		s.refuseTake(w, r, lib, http.StatusForbidden, "")
		return
	}

	err := s.store.UntakeItem(r.Context(), lib.ID, sess.ID, itemID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderInvalid(w)
		return
	case errors.Is(err, store.ErrShelfFull):
		s.refuseTake(w, r, lib, http.StatusConflict,
			"The shelf filled up while this was gone. It's in the shed — the steward can put it back.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, shelfURL(lib)+"#i-"+itemID, http.StatusSeeOther)
}

// refuseTake re-renders the shelf with a message, at the given status.
//
// Always the shelf, whichever page the control was pressed on. Carrying the
// origin page through the request would buy a slightly better return for a
// case that should not happen.
func (s *Server) refuseTake(w http.ResponseWriter, r *http.Request,
	lib boulevard.Library, status int, msg string) {
	s.renderShelf(w, r, lib, status, msg)
}
