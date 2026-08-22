package web

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"

	"github.com/gumptionthomas/boulevard/internal/booklet"
	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/store"
	"github.com/gumptionthomas/boulevard/internal/tokens"
	"github.com/gumptionthomas/boulevard/internal/version"
)

// tokenRow is one card as the page shows it.
//
// Secret is deliberately absent. A secret is the shelf's write credential
// (DESIGN.md §4), and this page is the one a steward is most likely to
// screenshot or hand to someone standing next to them. The way to put
// secrets in front of a person is the booklet, which is an artifact meant
// to carry them.
//
// Booklet and Card are derived rather than stored: rotation makes
// period_index monotonic (13..24, then 25..36), so the number printed on a
// card is its position within its own booklet.
type tokenRow struct {
	Period    int
	Booklet   int
	Card      int
	Month     string
	Year      int
	From      boulevard.Date
	Until     boulevard.Date
	State     boulevard.TokenState
	Seen      bool
	Active    bool
	CanForce  bool
	CanExtend bool
	CanRevoke bool
}

func (s *Server) handleStewardTokens(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.renderStewardTokens(w, r, lib, http.StatusOK, okMessages[r.URL.Query().Get("ok")], "")
}

func (s *Server) renderStewardTokens(w http.ResponseWriter, r *http.Request, lib boulevard.Library, status int, notice, errMsg string) {
	toks, err := s.store.TokensForLibrary(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	rows := make([]tokenRow, 0, len(toks))
	for _, tok := range toks {
		active := tok.State == boulevard.TokenActive
		rows = append(rows, tokenRow{
			Period:  tok.PeriodIndex,
			Booklet: (tok.PeriodIndex-1)/tokens.PeriodCount + 1,
			Card:    (tok.PeriodIndex-1)%tokens.PeriodCount + 1,
			Month:   tok.ValidFrom.MonthName(),
			Year:    tok.ValidFrom.Year,
			From:    tok.ValidFrom,
			Until:   tok.ValidUntil,
			State:   tok.State,
			Seen:    tok.FirstSeenAt != nil,
			Active:  active,
			// Actions are shown only where they are legal, so the page never
			// offers a tap that can only fail. This is a different reason
			// from a pinned item hiding its take control: there the action
			// does not exist for anyone, here it is this card's state that
			// makes it illegal.
			CanForce:  tok.State == boulevard.TokenPending,
			CanExtend: active,
			CanRevoke: tok.State != boulevard.TokenRevoked,
		})
	}

	s.render(w, status, "steward-tokens.html", stewardData{
		Title:       "Tokens — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
		Notice:      notice,
		Error:       errMsg,
		TokenRows:   rows,
		Now:         s.now(),
	})
}

// invalidPeriodMsg covers two distinct failures with one wording, on
// purpose: a period that does not parse as a number and a period that
// parses but names no card read the same to a steward standing at their
// box — "type this into the URL bar" is not a workflow this page offers,
// so there is no reason to tell the two apart.
const invalidPeriodMsg = "That card number isn't valid."

// periodFromPath parses {period} and re-renders the tokens page with the
// shared invalid-period message on failure, so all four mutations handle a
// mistyped or forged path value identically rather than panicking on
// strconv.Atoi's error.
func (s *Server) periodFromPath(w http.ResponseWriter, r *http.Request, lib boulevard.Library) (int, bool) {
	period, err := strconv.Atoi(r.PathValue("period"))
	if err != nil {
		s.renderStewardTokens(w, r, lib, http.StatusNotFound, "", invalidPeriodMsg)
		return 0, false
	}
	return period, true
}

// stateOf looks up one card's current state by period, for a refusal
// message that names what it found rather than just that it refused
// (DESIGN.md §5's reasoning for the all-pinned refusal, applied here too).
// ForceActivateToken's own error only says "not pending", not which state
// the card is actually in, so this asks again rather than parsing the
// sentinel's text.
func (s *Server) stateOf(r *http.Request, lib boulevard.Library, period int) (boulevard.TokenState, error) {
	toks, err := s.store.TokensForLibrary(r.Context(), lib.ID)
	if err != nil {
		return "", err
	}
	for _, tok := range toks {
		if tok.PeriodIndex == period {
			return tok.State, nil
		}
	}
	return "", store.ErrNotFound
}

// handleStewardForceActivate promotes a pending card to the one in the
// door, for the steward who swapped the physical card early (DESIGN.md
// §4). ForceActivateToken moves the card's valid_from to today; it does
// not touch valid_until, so the printed card and the database will now
// disagree about when this period ends — accepted, because the steward has
// already put the card in the door and reads it by month name.
func (s *Server) handleStewardForceActivate(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	period, ok := s.periodFromPath(w, r, lib)
	if !ok {
		return
	}

	err := s.store.ForceActivateToken(r.Context(), lib.ID, period, boulevard.DateFromTime(s.now()))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderStewardTokens(w, r, lib, http.StatusNotFound, "", invalidPeriodMsg)
		return
	case errors.Is(err, store.ErrNotPending):
		state, sErr := s.stateOf(r, lib, period)
		if sErr != nil {
			noStore(w)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		s.renderStewardTokens(w, r, lib, http.StatusConflict, "",
			"That card is already "+string(state)+". Only an unused card can be put in the door.")
		return
	case err != nil:
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"tokens", "activated"), http.StatusSeeOther)
}

// handleStewardExtend pushes the active card's end date out by a month,
// for the steward whose replacement booklet is not printed yet (DESIGN.md
// §4). Only the active card qualifies — a pending or expired card has
// nothing "in the door" to extend.
func (s *Server) handleStewardExtend(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	period, ok := s.periodFromPath(w, r, lib)
	if !ok {
		return
	}

	_, err := s.store.ExtendToken(r.Context(), lib.ID, period)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderStewardTokens(w, r, lib, http.StatusNotFound, "", invalidPeriodMsg)
		return
	case errors.Is(err, store.ErrNotActive):
		s.renderStewardTokens(w, r, lib, http.StatusConflict, "",
			"Only the card in the door can be extended.")
		return
	case err != nil:
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"tokens", "extended"), http.StatusSeeOther)
}

// handleStewardRevoke burns one card's secret, for a sheet that was stolen
// or photographed (DESIGN.md §4). There is no un-revoke: the way forward
// is force-activating the next card or rotating onto a new booklet.
func (s *Server) handleStewardRevoke(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	period, ok := s.periodFromPath(w, r, lib)
	if !ok {
		return
	}

	err := s.store.RevokeToken(r.Context(), lib.ID, period)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderStewardTokens(w, r, lib, http.StatusNotFound, "", invalidPeriodMsg)
		return
	case errors.Is(err, store.ErrAlreadyRevoked):
		s.renderStewardTokens(w, r, lib, http.StatusConflict, "", "That card is already revoked.")
		return
	case err != nil:
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"tokens", "revoked"), http.StatusSeeOther)
}

// handleStewardRotate discards every unprinted card and mints a fresh
// booklet, leaving the active card untouched. This builds the twelve new
// tokens exactly as `boulevard booklet --rotate` does (cmd/boulevard/
// booklet.go's buildRotatedTokens): TokensForLibrary, then
// tokens.PlanRotation to find where the next booklet starts, then
// tokens.Periods to lay out its twelve calendar windows. It cannot live in
// cmd/boulevard, an unrelated main package, so the same few lines exist
// twice — the CLI and the desk are two callers of the same store method,
// not one reusing the other.
func (s *Server) handleStewardRotate(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}

	existing, err := s.store.TokensForLibrary(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	rot := tokens.PlanRotation(existing, boulevard.DateFromTime(s.now()))
	periods := tokens.Periods(rot.Start, tokens.PeriodCount)
	fresh := make([]boulevard.Token, 0, len(periods))
	for _, p := range periods {
		secret, err := tokens.NewSecret(rand.Reader)
		if err != nil {
			noStore(w)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		tokID, err := boulevard.RandomBase32(rand.Reader, boulevard.EntropyBytes)
		if err != nil {
			noStore(w)
			http.Error(w, "database unavailable", http.StatusInternalServerError)
			return
		}
		fresh = append(fresh, boulevard.Token{
			ID: tokID, LibraryID: lib.ID, Secret: secret,
			PeriodIndex: rot.StartIndex + p.Index - 1,
			ValidFrom:   p.From, ValidUntil: p.Until,
			State: boulevard.TokenPending,
		})
	}

	if err := s.store.RotatePendingTokens(r.Context(), lib.ID, fresh); err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	noStore(w)
	http.Redirect(w, r, withOK(stewardPath(lib)+"tokens", "rotated"), http.StatusSeeOther)
}

// bookletTokensMsg is the refusal shown when the library does not hold a
// clean twelve-card booklet to print — a rotation half-applied, or a row
// deleted out from under it.
const bookletTokensMsg = "This booklet is incomplete. Rotate to mint a fresh twelve."

// handleStewardBookletPDF renders the current booklet and sends it.
//
// The twelve cards are the highest-numbered booklet the library holds:
// rotation makes period_index monotonic, and BuildPlan requires exactly
// twelve tokens. Reprinting an older booklet is not offered — its cards are
// either in the door already or discarded.
//
// This response carries every card's secret across the network, and serve
// terminates no TLS. The page linking here says so; withholding the
// capability would take away the thing this milestone exists to provide,
// and the steward key already crosses the same wire on every request.
//
// The work is bounded by design, which matters because SetMaxOpenConns(1)
// serialises every request through one connection: a booklet is always
// twelve cards, never more.
func (s *Server) handleStewardBookletPDF(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}

	all, err := s.store.TokensForLibrary(r.Context(), lib.ID)
	if err != nil {
		noStore(w)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	maxIndex := 0
	for _, tok := range all {
		if tok.PeriodIndex > maxIndex {
			maxIndex = tok.PeriodIndex
		}
	}
	start := ((maxIndex-1)/tokens.PeriodCount)*tokens.PeriodCount + 1

	toks := make([]boulevard.Token, 0, tokens.PeriodCount)
	for _, tok := range all {
		if tok.PeriodIndex >= start {
			toks = append(toks, tok)
		}
	}
	if len(toks) != tokens.PeriodCount {
		s.renderStewardTokens(w, r, lib, http.StatusConflict, "", bookletTokensMsg)
		return
	}

	plan, err := booklet.BuildPlan(booklet.Input{
		Library:   lib,
		Tokens:    toks,
		SourceURL: version.RepoURL,
		BuildLine: "boulevard " + version.Version + " (" + version.Commit + ")",
	})
	if err != nil {
		noStore(w)
		http.Error(w, "cannot build the booklet", http.StatusInternalServerError)
		return
	}

	pdf, err := booklet.Renderer{CreationDate: s.now()}.Render(plan)
	if err != nil {
		noStore(w)
		http.Error(w, "cannot render the booklet", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lib.Slug+`-booklet.pdf"`)
	noStore(w)
	w.WriteHeader(http.StatusOK)
	w.Write(pdf)
}
