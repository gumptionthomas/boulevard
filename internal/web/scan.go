package web

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
)

// handleScan is the endpoint printed on twelve cards. It resolves the
// secret, decides the outcome, and either mints a session or explains why it
// did not.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	tok, err := s.store.TokenBySecret(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderInvalid(w)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	switch tokens.Validate(tok, boulevard.DateFromTime(now), tokens.GraceDays) {
	case tokens.Invalid:
		s.renderInvalid(w)
	case tokens.OutOfWindow:
		s.renderOutOfWindow(w, r, tok, now)
	case tokens.Granted:
		s.grant(w, r, tok, now)
	}
}

// renderInvalid serves the generic failure page.
//
// It must render identically for an unknown secret and a revoked one. An
// unknown secret resolves to no library at all, and naming a library only in
// the revoked case would confirm to whoever photographed a card that the
// secret was real (spec §4.2). No name, no branding, no shelf link.
func (s *Server) renderInvalid(w http.ResponseWriter) {
	s.render(w, http.StatusNotFound, "invalid.html", pageData{Title: "Not valid"})
}

func (s *Server) renderOutOfWindow(w http.ResponseWriter, r *http.Request, tok boulevard.Token, now time.Time) {
	lib, err := s.store.LibraryByID(r.Context(), tok.LibraryID)
	if err != nil {
		s.renderInvalid(w)
		return
	}
	// 200, not an error status: DESIGN.md §4 is explicit that this is
	// diagnostic information the steward needs, distinct from failure.
	s.render(w, http.StatusOK, "outdated.html", pageData{
		Title:       "Card out of date",
		LibraryName: lib.Name,
		Location:    lib.LocationLabel,
		CardLabel:   tok.ValidFrom.MonthName() + " " + strconv.Itoa(tok.ValidFrom.Year),
		StoppedOn:   humanDate(tok.ValidUntil.AddDays(tokens.GraceDays)),
		ShelfURL:    "/b/" + lib.Slug + "/",
	})
}

func (s *Server) grant(w http.ResponseWriter, r *http.Request, tok boulevard.Token, now time.Time) {
	lib, err := s.store.LibraryByID(r.Context(), tok.LibraryID)
	if err != nil {
		s.renderInvalid(w)
		return
	}
	if err := s.store.RecordScan(r.Context(), lib.ID, tok, now); err != nil {
		http.Error(w, "could not record the scan", http.StatusInternalServerError)
		return
	}

	id, err := boulevard.NewSessionID(rand.Reader)
	if err != nil {
		http.Error(w, "could not mint a session", http.StatusInternalServerError)
		return
	}
	sess := boulevard.Session{
		ID: id, LibraryID: lib.ID, TokenID: tok.ID,
		CreatedAt: now, ExpiresAt: now.Add(boulevard.SessionTTL),
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		http.Error(w, "could not store the session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    string(id),
		Path:     "/",
		MaxAge:   int(boulevard.SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	http.Redirect(w, r, "/b/"+lib.Slug+"/", http.StatusFound)
}
