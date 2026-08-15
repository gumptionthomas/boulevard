package web

import "net/http"

// Handler wires the public surface.
//
// Two of these patterns are not design choices: Milestone 0 printed twelve
// cards encoding {base_url}/s/{secret} and a mounted sign encoding
// {base_url}. Those URLs exist on paper, and changing them invalidates a
// booklet.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /b/{slug}/{$}", s.handleShelf)
	mux.HandleFunc("GET /b/{slug}/about", s.handleAbout)
	mux.HandleFunc("GET /s/{token}", s.handleScan)
	return mux
}
