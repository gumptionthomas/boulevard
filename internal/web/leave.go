package web

import (
	"crypto/rand"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

// maxLeaveBody caps the only route on this server that accepts a write from
// a stranger.
//
// Go's default ceiling for a urlencoded body is 10 MB, and ParseForm reads
// all of it into memory before any of the 4,000/1,000/120-rune caps can
// apply. Every request already serializes through one SQLite connection
// (SetMaxOpenConns(1), DESIGN.md §2) on a box a non-expert runs at home, so
// concurrent large POSTs from one shared card are a cheap way to make the
// shelf stop answering. 64 KiB is roughly ten times the sum of every cap,
// which leaves multi-byte scripts their full allowance and nothing else.
const maxLeaveBody = 64 << 10

// handleLeaveForm renders the form.
//
// Without a session it renders anyway, inert and carrying §6's explanation,
// rather than 404ing: show the rule, do not hide the feature. Milestone 1
// established this for the shelf's controls.
func (s *Server) handleLeaveForm(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	sess, live := s.liveSession(r, lib.ID)
	s.renderLeave(w, lib, http.StatusOK,
		boulevard.Submission{Type: string(boulevard.ItemLink)}, nil, sess, live)
}

// handleLeaveSubmit stores what was left, or explains why it did not.
//
// The form is read before the session is checked, so that a 403 can hand
// back what was composed. DESIGN.md §4 sells the 24-hour window as "scan on
// your walk, write at your kitchen table", which makes a session expiring
// between loading this form and submitting it the intended usage pattern
// meeting its boundary — not an edge case. The 422 path below already
// treats losing a composed note as this form's worst failure; the 403 path
// loses the same note from the same person.
//
// Re-rendering it populated is safe: the form is inert without a session,
// and nothing has been stored.
func (s *Server) handleLeaveSubmit(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	in := boulevard.Submission{
		Type:        r.PostFormValue("type"),
		Payload:     r.PostFormValue("payload"),
		Note:        r.PostFormValue("note"),
		Attribution: r.PostFormValue("attribution"),
	}

	sess, live := s.liveSession(r, lib.ID)
	if !live {
		// 403 rather than a redirect: the person is not at the box, and a
		// redirect would suggest the submission is retryable as-is.
		s.renderLeave(w, lib, http.StatusForbidden, in, nil, sess, live)
		return
	}

	clean, errs := boulevard.ValidateSubmission(in)
	if len(errs) > 0 {
		// Re-render with what they typed. Losing a composed note to a
		// validation error is the worst failure this form has.
		s.renderLeave(w, lib, http.StatusUnprocessableEntity, clean, errs, sess, live)
		return
	}

	id, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
	if err != nil {
		http.Error(w, "could not store it", http.StatusInternalServerError)
		return
	}
	item := boulevard.Item{
		ID: id, LibraryID: lib.ID,
		Type: boulevard.ItemType(clean.Type), Payload: clean.Payload,
		Note: clean.Note, Attribution: clean.Attribution,
		State: boulevard.ItemPending, LeftAt: s.now(),
	}
	if err := s.store.CreateItem(r.Context(), lib.ID, item); err != nil {
		http.Error(w, "could not store it", http.StatusInternalServerError)
		return
	}

	// 303 so a reload cannot leave the same thing twice.
	noStore(w)
	http.Redirect(w, r, leftURL(lib), http.StatusSeeOther)
}

func (s *Server) handleLeft(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "left.html", pageData{
		Title:       "Left at the box",
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
	})
}

// renderLeave draws the form. The session is passed in rather than looked up
// again: every caller has already resolved it, and asking the store twice on
// one request is a second round trip through the single write connection for
// an answer that cannot have changed.
func (s *Server) renderLeave(w http.ResponseWriter, lib boulevard.Library, status int,
	form boulevard.Submission, errs boulevard.FieldErrors, sess boulevard.Session, live bool) {

	data := pageData{
		Title:       "Leave something",
		LibraryName: lib.Name,
		LeaveURL:    leaveURL(lib),
		ShelfURL:    shelfURL(lib),
		Form:        form,
		Errors:      errs,
	}
	if live {
		data.HasSession = true
		data.Deadline = humanDeadline(sess.ExpiresAt, s.now())
	}
	s.render(w, status, "leave.html", data)
}
