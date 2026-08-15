package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// handleItem renders one item. §6 calls it shareable: it is what a link out
// of the shelf points at.
//
// It counts a view. §7 keeps views and takes as separate numbers because
// they answer different questions — "the internet found this" versus "three
// neighbours wanted it" — and views deliberately drive nothing at all.
// Neither is displayed yet: until takes can move, every item would read
// "Taken 0 times".
func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	// Both sweeps, on every HTML read path that can show an item. The item
	// page is easy to forget and would otherwise serve an expired item at
	// its own shareable URL forever: an unswept item is still `shelved`, so
	// the state filter below passes it. A link that outlives the shelf
	// listing is exactly what expiry exists to prevent.
	if _, err := s.store.SweepExpiredSessions(r.Context(), s.now()); err != nil {
		log.Printf("sessions not swept: %v", err)
	}
	if _, err := s.store.SweepExpiredItems(r.Context(), lib.ID, s.now()); err != nil {
		log.Printf("items not swept: %v", err)
	}

	it, err := s.store.ItemByID(r.Context(), lib.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	// ItemByID is library-scoped only, on purpose — the CLI and future admin
	// views need to load an item in any state. This handler is the public,
	// shareable one, so it enforces §5's "not public" rule for anything not
	// shelved: pending items skip the approval gate otherwise, and shed or
	// released items stay reachable at a URL DESIGN.md says is not public.
	if it.State != boulevard.ItemShelved {
		http.NotFound(w, r)
		return
	}

	// A view failing to record is not worth failing the page over, but a
	// real fault should still leave a trace — only the benign race where
	// the item vanished between ItemByID and here (ErrNotFound) is silent.
	if err := s.store.IncrementViews(r.Context(), lib.ID, it.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("view not recorded: %v", err)
	}

	itemBase := itemURL(lib, "")
	view := itemView{Item: it, ItemBase: itemBase}
	live := false
	if sess, ok := s.liveSession(r, lib.ID); ok {
		live = true
		view.HasSession = true
		// A take-state failure is not worth failing the page over either —
		// same reasoning as the view count above. The control just falls
		// back to its normal "Take" state, which TakeItem's own idempotent
		// duplicate-take guard keeps safe.
		taken, err := s.store.TakenBySession(r.Context(), sess.ID)
		if err != nil {
			log.Printf("takes not loaded: %v", err)
		} else {
			view.TakenByYou = taken[it.ID]
			view.AtLimit = len(taken) >= maxTakes
		}
	}

	s.render(w, http.StatusOK, "item.html", pageData{
		Title:       lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		ItemBase:    itemBase,
		// HasSession here (not just on the view) is what item.html needs to
		// show §6's explanation when there is no session: without it, a
		// remote reader saw a greyed "Take" control and nothing telling
		// them why (I-3).
		HasSession: live,
		Item:       view,
	})
}
