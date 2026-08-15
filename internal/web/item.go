package web

import (
	"errors"
	"net/http"

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
	it, err := s.store.ItemByID(r.Context(), lib.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// A view failing to record is not worth failing the page over.
	_ = s.store.IncrementViews(r.Context(), lib.ID, it.ID)

	s.render(w, http.StatusOK, "item.html", pageData{
		Title:       lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    "/b/" + lib.Slug + "/",
		Item:        it,
	})
}
