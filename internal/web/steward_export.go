package web

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// handleStewardExport renders the page that links to the download. It
// exists separately from the download itself so the plaintext warning has
// somewhere to be read before the file starts moving.
func (s *Server) handleStewardExport(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "steward-export.html", stewardData{
		Title:       "Export — " + lib.Name,
		LibraryName: lib.Name,
		ShelfURL:    shelfURL(lib),
		StewardURL:  stewardPath(lib),
		Library:     lib,
	})
}

// handleStewardExportDownload writes the library to a temporary file and
// sends it. CopyLibraryTo needs a path — it opens the target through
// store.Open so the export gets the same migrations and the same 0600 mode
// as boulevard.db, which a streaming writer could not provide.
//
// The work is bounded by design: a library's rows (items capped at `slots`,
// tokens always twelve to twenty-some) never grow with traffic or age, so
// there is nothing here that could block the one connection
// SetMaxOpenConns(1) serialises every request through.
func (s *Server) handleStewardExportDownload(w http.ResponseWriter, r *http.Request) {
	lib, _, ok := s.requireSteward(w, r)
	if !ok {
		return
	}
	dir, err := os.MkdirTemp("", "boulevard-export")
	if err != nil {
		noStore(w)
		http.Error(w, "cannot write the export", http.StatusInternalServerError)
		return
	}
	// The export carries every card's secret in plaintext, so the temp
	// directory must not survive the request. MkdirTemp's 0700 keeps it
	// unreadable to anyone else on the machine in the meantime, and this
	// deferred removal is what keeps it from accumulating in /tmp.
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, lib.Slug+".db")
	if err := s.store.CopyLibraryTo(r.Context(), lib.ID, path); err != nil {
		noStore(w)
		http.Error(w, "cannot write the export", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		noStore(w)
		http.Error(w, "cannot read the export", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+lib.Slug+`.db"`)
	noStore(w)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}
