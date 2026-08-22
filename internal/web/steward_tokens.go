package web

import (
	"net/http"

	"github.com/gumptionthomas/boulevard/internal/boulevard"
	"github.com/gumptionthomas/boulevard/internal/tokens"
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
