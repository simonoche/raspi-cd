package server

import (
	"context"
	"crypto/subtle"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"raspicd/internal/utils"
)

// Server is the central control server.
type Server struct {
	bindAddr     string
	version      string
	ciSecret     string // used by CI/CD pipelines to create/read tasks
	agentSecret  string // used by agents to heartbeat, fetch tasks, and report results
	agentTimeout time.Duration
	staticFS     fs.FS
	store        store
	notifier     *notifier
	router       *http.ServeMux
	httpSrv      *http.Server
	cancel       context.CancelFunc
}

// New creates and configures a Server.
func New(bindAddr, ciSecret, agentSecret, version string, agentTimeout time.Duration, staticFS fs.FS) *Server {
	s := &Server{
		bindAddr:     bindAddr,
		version:      version,
		ciSecret:     ciSecret,
		agentSecret:  agentSecret,
		agentTimeout: agentTimeout,
		staticFS:     staticFS,
		store:        newMemStore(),
		notifier:     newNotifier(),
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
		Handler: s.withVersion(s.router),
	}
	utils.Logger.Infof("Server listening on %s (agent timeout: %s)", s.bindAddr, s.agentTimeout)
	return s.httpSrv.ListenAndServe()
}

// Stop shuts down the server.
func (s *Server) Stop() error {
	utils.Logger.Info("Server shutting down")
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
	utils.Logger.Debugf("Stale agent sweep every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, id := range s.store.markStaleAgents(s.agentTimeout) {
				utils.Logger.Warnf("Agent %s marked offline (no heartbeat for >%s)", id, s.agentTimeout)
			}
		}
	}
}

func (s *Server) routes() {
	// Unauthenticated.
	s.router.HandleFunc("/", s.handleIndex)
	s.router.HandleFunc("/health", s.handleHealth)

	// Agent-facing (agent secret).
	s.router.HandleFunc("/api/v1/agents/heartbeat", s.authAgent(s.handleHeartbeat))
	s.router.HandleFunc("/api/v1/agents/{id}/disconnect", s.authAgent(s.handleAgentDisconnect))
	s.router.HandleFunc("/api/v1/agents/{id}/tasks", s.authAgent(s.handleAgentTasks))
	s.router.HandleFunc("/api/v1/tasks/{id}/result", s.authAgent(s.handleTaskResult))

	// CI/CD and admin facing (CI secret).
	s.router.HandleFunc("/api/v1/agents", s.authCI(s.handleAgents))
	s.router.HandleFunc("/api/v1/tasks/broadcast", s.authCI(s.handleBroadcastTask))
	s.router.HandleFunc("/api/v1/tasks", s.authCI(s.handleTasks))
	s.router.HandleFunc("/api/v1/tasks/{id}", s.authCI(s.handleTask))
}

// withVersion wraps a handler to set X-RasPiCD-Version on every response.
func (s *Server) withVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RasPiCD-Version", s.version)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authAgent(next http.HandlerFunc) http.HandlerFunc {
	return s.authWith(s.agentSecret, next)
}

func (s *Server) authCI(next http.HandlerFunc) http.HandlerFunc {
	return s.authWith(s.ciSecret, next)
}

// authWith wraps a handler requiring a specific Bearer token.
// Uses constant-time comparison to prevent timing attacks.
func (s *Server) authWith(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, _ := strings.CutPrefix(header, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
			utils.Logger.Warnf("Unauthorized %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
