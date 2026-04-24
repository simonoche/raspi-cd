package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"raspideploy/internal/utils"
)

// Server is the central control server.
type Server struct {
	bindAddr     string
	secret       string
	agentTimeout time.Duration
	store        store
	router       *http.ServeMux
	httpSrv      *http.Server
	cancel       context.CancelFunc
}

// New creates and configures a Server.
func New(bindAddr, secret string, agentTimeout time.Duration) *Server {
	s := &Server{
		bindAddr:     bindAddr,
		secret:       secret,
		agentTimeout: agentTimeout,
		store:        newMemStore(),
		router:       http.NewServeMux(),
	}
	s.routes()
	return s
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.staleSweep(ctx)

	s.httpSrv = &http.Server{
		Addr:    s.bindAddr,
		Handler: s.router,
	}
	utils.Logger.Infof("server listening on %s (agent timeout: %s)", s.bindAddr, s.agentTimeout)
	return s.httpSrv.ListenAndServe()
}

// Stop shuts down the server.
func (s *Server) Stop() error {
	utils.Logger.Info("server shutting down")
	if s.cancel != nil {
		s.cancel()
	}
	if s.httpSrv != nil {
		return s.httpSrv.Close()
	}
	return nil
}

// staleSweep runs in the background and marks agents offline when they stop
// sending heartbeats. It ticks at agentTimeout/3 so a stale agent is caught
// within one extra timeout window regardless of how short the timeout is.
func (s *Server) staleSweep(ctx context.Context) {
	interval := s.agentTimeout / 3
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	utils.Logger.Debugf("stale agent sweep every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, id := range s.store.markStaleAgents(s.agentTimeout) {
				utils.Logger.Warnf("agent %s marked offline (no heartbeat for >%s)", id, s.agentTimeout)
			}
		}
	}
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
			utils.Logger.Warnf("unauthorized %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
