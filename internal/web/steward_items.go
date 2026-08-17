package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
)

// maxPins is DESIGN.md §5's pin ceiling: 3, fixed for every library, unlike
// `slots` which the steward sets. PinItem takes it as a parameter rather
// than reading it from settings for that reason.
const maxPins = 3

// maxNoticeNoteRunes bounds how much of an item's note a refusal message
// quotes. A note is capped at 1,000 runes but not at one line (see
// boulevard.MaxNoteRunes and cmd/boulevard/queue.go's firstLine), so naming
// three pinned items without a cap could turn one refusal into a wall of
// text a steward has to scroll past standing at their box.
const maxNoticeNoteRunes = 60

// okMessages is the closed set of confirmations a successful mutation's
// redirect may carry, keyed by the `?ok=` code the redirect target's query
// string carries instead of free text (see withOK's doc comment for why).
// A code outside this map — including an absent one — looks up to the zero
// value and renders nothing; that "renders nothing" default, not an error
// page, is what keeps a mistyped or attacker-supplied code inert rather
// than surfacing as broken UI.
var okMessages = map[string]string{
	"shelved":         "Shelved.",
	"shelved-evicted": "Shelved. The shelf was full, so the oldest item moved to the shed.",
	"rejected":        "Released. The row stays, so nothing is lost.",
	"removed":         "Removed. It's in the shed — reshelve it if this was a misfire.",
	"pinned":          "Pinned.",
	"unpinned":        "Unpinned.",
	"reshelved":       "Reshelved.",
	"released":        "Released. The row stays, so nothing is lost.",
}

// handleStewardQueue lists what is waiting for a decision.
func (s *Server) handleStewardQueue(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardQueue(w, r, lib, http.StatusOK, okMessages[r.URL.Query().Get("ok")], "")
}

// handleStewardShelf lists what is on the shelf, with pin/unpin/remove.
func (s *Server) handleStewardShelf(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardShelf(w, r, lib, http.StatusOK, okMessages[r.URL.Query().Get("ok")], "")
}

// handleStewardShed lists what has been shed, with reshelve/release.
func (s *Server) handleStewardShed(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardShed(w, r, lib, http.StatusOK, okMessages[r.URL.Query().Get("ok")], "")
}

func (s *Server) renderStewardQueue(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string) {
	pending, err := s.store.PendingItems(r.Context(), lib.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	s.render(w, status, "steward-queue.html", stewardData{
		Title:       "Waiting — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Items:       pending,
		Notice:      notice,
		Error:       errMsg,
		Now:         s.now(),
	})
}

func (s *Server) renderStewardShelf(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string) {
	shelved, err := s.store.ShelvedItems(r.Context(), lib.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	s.render(w, status, "steward-shelf.html", stewardData{
		Title:       "Shelf — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Items:       shelved,
		Slots:       lib.Slots,
		Notice:      notice,
		Error:       errMsg,
		Now:         s.now(),
	})
}

func (s *Server) renderStewardShed(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string) {
	shed, err := s.store.ShedItems(r.Context(), lib.ID)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	s.render(w, status, "steward-shed.html", stewardData{
		Title:       "Shed — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Items:       shed,
		Notice:      notice,
		Error:       errMsg,
		Now:         s.now(),
	})
}

// notPending, notShelved and notShed are the shared re-render copy for the
// three "the item moved out from under you" sentinels. Not a bare error
// string (ErrNotPending.Error() etc.): a steward who cannot act needs to
// know what would let them act, and the store's own wording ("not pending",
// "is shed") is written for a log line, not a person standing at their box.
const (
	notPendingMsg = "That item isn't waiting anymore — someone already decided it."
	notShelvedMsg = "That item isn't on the shelf anymore."
	notShedMsg    = "That item isn't in the shed anymore."
)

// handleStewardApprove shelves a pending item.
//
// ApproveItem's ErrAllPinned is the branch this milestone makes reachable
// (store/item.go's own comment: unreachable until PinItem existed to set
// `pinned`) — a full shelf where every item is pinned has nothing to evict,
// and the refusal must land here as a re-rendered page, not a 500. A count
// alone does not tell the steward which item is blocking, so PinnedItems
// follows to name them (DESIGN.md §5/§6).
func (s *Server) handleStewardApprove(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	evicted, err := s.store.ApproveItem(r.Context(), lib.ID, id, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotPending):
		s.renderStewardQueue(w, r, lib, http.StatusConflict, "", notPendingMsg)
		return
	case errors.Is(err, store.ErrAllPinned):
		pinned, pErr := s.store.PinnedItems(r.Context(), lib.ID)
		if pErr != nil {
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		s.renderStewardQueue(w, r, lib, http.StatusConflict, "",
			"The shelf is full and every item on it is pinned: "+namePinned(pinned)+
				". Unpin one, or raise the slot count, before approving this.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	code := "shelved"
	if evicted != "" {
		code = "shelved-evicted"
	}
	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"queue", code), http.StatusSeeOther)
}

// handleStewardReject releases a pending item. The row stays — §5's soft
// delete — so a steward who rejects the wrong thing has not destroyed it.
func (s *Server) handleStewardReject(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.RejectItem(r.Context(), lib.ID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotPending):
		s.renderStewardQueue(w, r, lib, http.StatusConflict, "", notPendingMsg)
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"queue", "rejected"), http.StatusSeeOther)
}

// handleStewardRemove sheds a shelved item, with reason "removed". It sheds
// rather than releases: a misfire must be recoverable by re-shelving, and
// `release` stays the deliberate second step (DESIGN.md §5).
func (s *Server) handleStewardRemove(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.RemoveItem(r.Context(), lib.ID, id, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotShelved):
		s.renderStewardShelf(w, r, lib, http.StatusConflict, "", notShelvedMsg)
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"shelf", "removed"), http.StatusSeeOther)
}

// handleStewardPin marks a shelved item as furniture: never evicted, never
// expired, not takeable. ErrPinLimit is the fourth pin — DESIGN.md §5 caps
// pins at 3 — and names the three already pinned, the same reasoning as
// handleStewardApprove's ErrAllPinned branch.
func (s *Server) handleStewardPin(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.PinItem(r.Context(), lib.ID, id, maxPins)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotShelved):
		s.renderStewardShelf(w, r, lib, http.StatusConflict, "", notShelvedMsg)
		return
	case errors.Is(err, store.ErrPinLimit):
		pinned, pErr := s.store.PinnedItems(r.Context(), lib.ID)
		if pErr != nil {
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		s.renderStewardShelf(w, r, lib, http.StatusConflict, "",
			"Three items are already pinned: "+namePinned(pinned)+". Unpin one before pinning another.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"shelf", "pinned"), http.StatusSeeOther)
}

// handleStewardUnpin returns an item to ordinary stock: evictable,
// expirable, takeable again.
func (s *Server) handleStewardUnpin(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.UnpinItem(r.Context(), lib.ID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotShelved):
		s.renderStewardShelf(w, r, lib, http.StatusConflict, "", notShelvedMsg)
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"shelf", "unpinned"), http.StatusSeeOther)
}

// handleStewardReshelve returns a shed item to the shelf. ErrShelfFull is
// ReshelveItem refusing rather than evicting — the steward asked to add one
// item back, not to remove another (§5's warning against mechanics that
// generate steward labor applies here too).
func (s *Server) handleStewardReshelve(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.ReshelveItem(r.Context(), lib.ID, id, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotShed):
		s.renderStewardShed(w, r, lib, http.StatusConflict, "", notShedMsg)
		return
	case errors.Is(err, store.ErrShelfFull):
		s.renderStewardShed(w, r, lib, http.StatusConflict, "",
			"The shelf is full. Remove something, or raise the slot count, before reshelving this.")
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"shed", "reshelved"), http.StatusSeeOther)
}

// handleStewardRelease soft-deletes a shed item. The row stays, so a
// steward who releases the wrong thing has not destroyed it.
func (s *Server) handleStewardRelease(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	err := s.store.ReleaseItem(r.Context(), lib.ID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrNotShed):
		s.renderStewardShed(w, r, lib, http.StatusConflict, "", notShedMsg)
		return
	case err != nil:
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"shed", "released"), http.StatusSeeOther)
}

// withOK appends a confirmation *code* — never free text — onto a redirect
// target, so a successful mutation can still land back on a 303 (a reload
// must not repeat the POST — the same reason handleTake redirects rather
// than renders) while carrying something worth telling the steward, such as
// ApproveItem's silent side effect of shedding the oldest item to make
// room. It went through a query parameter carrying the *message* first —
// review flagged that as a Critical: html/template escaping stops script,
// not text, so arbitrary prose in the app's own voice, at its own genuine
// URL, on the steward's own authenticated page, is a phishing primitive
// that SameSite=Strict does not contain (the cookie still rides along on a
// pasted URL, a link opened from SMS or Signal, or a same-origin route —
// item.html links a stranger's submitted URL verbatim, so a leaver could
// point one at this box's own steward URL with a crafted message attached).
// An enumerated code closes that off structurally: okMessages is the only
// place free text enters, the code space is small and fixed, and an
// unrecognized code — forged, stale, or simply mistyped — renders nothing
// rather than something merely sanitized.
func withOK(path, code string) string {
	return path + "?ok=" + code
}

// namePinned formats pinned items for a refusal message. A count does not
// tell a steward which item is blocking them — the entire point of the
// message (DESIGN.md §5/§6) — so this names each one by its note, the same
// field the shelf and queue pages themselves lead with.
func namePinned(items []boulevard.Item) string {
	notes := make([]string, len(items))
	for i, it := range items {
		notes[i] = `"` + truncateNote(it.Note) + `"`
	}
	return strings.Join(notes, ", ")
}

// truncateNote keeps one pinned item's contribution to a refusal message to
// one short line. A note is capped at 1,000 runes but not at one line
// (boulevard.MaxNoteRunes), so naming three of them without this could turn
// one refusal into a wall of text.
func truncateNote(note string) string {
	if i := strings.IndexByte(note, '\n'); i >= 0 {
		note = note[:i]
	}
	if r := []rune(note); len(r) > maxNoticeNoteRunes {
		return string(r[:maxNoticeNoteRunes]) + "…"
	}
	return note
}
