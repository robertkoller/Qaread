package server

import (
	"context"
	"net/http"
)

type Server struct {
	server *http.Server
}

func (server *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/capture", server.handleCapture)

	serv := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}
	server.server = serv

	return server.server.ListenAndServe()
}

// Stop gracefully shuts the server down so Start returns.
func (server *Server) Stop() {
	server.server.Shutdown(context.Background()) //nolint:errcheck
}

func (server *Server) handleCapture(w http.ResponseWriter, r *http.Request) {

}
