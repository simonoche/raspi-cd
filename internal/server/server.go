package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"raspideploy/internal/utils"
)

// Server is the central control server.
type Server struct {
	bindAddr string
	secret   string
	store    store
	router   *http.ServeMux
	httpSrv  *http.Server
}

// New creates and configures a Server.
func New(bindAddr, secret string) *Server {
	s := &Server{
		bindAddr: bindAddr,
		secret:   secret,
		store:    newMemStore(),
		router:   http.NewServeMux(),
	}
	s.routes()
	return s
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	s.httpSrv = &http.Server{
		Addr:    s.bindAddr,
		Handler: s.router,
	}
	utils.Logger.Infof("server listening on %s", s.bindAddr)
	return s.httpSrv.ListenAndServe()
}

// Stop shuts down the server.
func (s *Server) Stop() error {
	if s.httpSrv != nil {
		return s.httpSrv.Close()
	}
	return nil
}

func (s *Server) routes() {
	// Unauthenticated.
	s.router.HandleFunc("/health", s.handleHealth)

	// Agent-facing.
	s.router.HandleFunc("/api/v1/agents/heartbeat", s.auth(s.handleHeartbeat))
	s.router.HandleFunc("/api/v1/agents/{id}/tasks", s.auth(s.handleAgentTasks))

	// CI/CD and admin facing.
	s.router.HandleFunc("/api/v1/agents", s.auth(s.handleAgents))
	s.router.HandleFunc("/api/v1/tasks", s.auth(s.handleTasks))
	s.router.HandleFunc("/api/v1/tasks/{id}/result", s.auth(s.handleTaskResult))
	s.router.HandleFunc("/api/v1/tasks/{id}", s.auth(s.handleTask))
}

// auth wraps a handler requiring a valid Bearer token.
// Uses constant-time comparison to prevent timing attacks.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, _ := strings.CutPrefix(header, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
