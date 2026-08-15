package web

import (
	"crypto/rand"
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
)

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
	s.renderLeave(w, r, lib, http.StatusOK, boulevard.Submission{Type: string(boulevard.ItemLink)}, nil)
}

func (s *Server) handleLeaveSubmit(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	if _, live := s.liveSession(r, lib.ID); !live {
		// 403 rather than a redirect: the person is not at the box, and a
		// redirect would suggest the submission is retryable as-is.
		s.renderLeave(w, r, lib, http.StatusForbidden,
			boulevard.Submission{Type: string(boulevard.ItemLink)}, nil)
		return
	}
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
	clean, errs := boulevard.ValidateSubmission(in)
	if len(errs) > 0 {
		// Re-render with what they typed. Losing a composed note to a
		// validation error is the worst failure this form has.
		s.renderLeave(w, r, lib, http.StatusUnprocessableEntity, clean, errs)
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
	http.Redirect(w, r, "/b/"+lib.Slug+"/left", http.StatusSeeOther)
}

func (s *Server) handleLeft(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.libraryFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "left.html", pageData{
		Title:       "Left at the box",
		LibraryName: lib.Name,
		ShelfURL:    "/b/" + lib.Slug + "/",
	})
}

func (s *Server) renderLeave(w http.ResponseWriter, r *http.Request, lib boulevard.Library,
	status int, form boulevard.Submission, errs boulevard.FieldErrors) {

	data := pageData{
		Title:       "Leave something",
		LibraryName: lib.Name,
		LeaveURL:    "/b/" + lib.Slug + "/leave",
		ShelfURL:    "/b/" + lib.Slug + "/",
		Form:        form,
		Errors:      errs,
	}
	if sess, live := s.liveSession(r, lib.ID); live {
		data.HasSession = true
		data.Deadline = humanDeadline(sess.ExpiresAt, s.now())
	}
	s.render(w, status, "leave.html", data)
}
