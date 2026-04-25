package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)

// ---- index -----------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, s.staticFS, "index.html")
}

// ---- /api/v1/pubkey --------------------------------------------------------

// handlePubKey serves GET /api/v1/pubkey — unauthenticated so agents can
// fetch the verification key on startup.
func (s *Server) handlePubKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.PublicKeyHex()})
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.Unmarshal(body, v)
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	utils.Logger.Warnf("Method not allowed: %s %s", r.Method, r.URL.Path)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// ---- /health ---------------------------------------------------------------

// handleHealth is intentionally silent — it is polled frequently by load
// balancers and Docker health checks.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// ---- /api/v1/agents --------------------------------------------------------

// handleAgents serves GET /api/v1/agents — list all registered agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}
	agents := s.store.listAgents()
	utils.Logger.Debugf("List agents: %d registered", len(agents))
	writeJSON(w, http.StatusOK, agents)
}

// ---- /api/v1/tasks ---------------------------------------------------------

// handleBroadcastTask serves POST /api/v1/tasks/broadcast.
// It creates one pending task per online agent and returns all task IDs.
func (s *Server) handleBroadcastTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}
	var req models.BroadcastTaskRequest
	if err := readJSON(r, &req); err != nil || req.Script == "" {
		utils.Logger.Warnf("Broadcast task: invalid request body from %s", r.RemoteAddr)
		http.Error(w, "script is required", http.StatusBadRequest)
		return
	}

	online := make([]*models.Agent, 0)
	for _, a := range s.store.listAgents() {
		if a.Status == "online" {
			online = append(online, a)
		}
	}
	if len(online) == 0 {
		http.Error(w, "no online agents", http.StatusConflict)
		return
	}

	results := make([]models.BroadcastTaskItem, 0, len(online))
	for _, a := range online {
		task := &models.Task{
			ID:        newID(),
			Script:    req.Script,
			Config:    req.Config,
			Status:    models.TaskStatusPending,
			AgentID:   a.ID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.signTask(task); err != nil {
			utils.Logger.Errorf("Sign task: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		s.store.createTask(task)
		s.hub.push(a.ID, task)
		results = append(results, models.BroadcastTaskItem{AgentID: a.ID, TaskID: task.ID})
	}
	utils.Logger.Infof("Broadcast script=%s created for %d agent(s)", req.Script, len(results))
	writeJSON(w, http.StatusCreated, results)
}

// handleTasks serves GET and POST /api/v1/tasks.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentID := r.URL.Query().Get("agent_id")
		status := r.URL.Query().Get("status")
		tasks := s.store.listTasks(agentID, status)
		utils.Logger.Debugf("List tasks: %d results (agent_id=%q status=%q)", len(tasks), agentID, status)
		writeJSON(w, http.StatusOK, tasks)

	case http.MethodPost:
		var req models.CreateTaskRequest
		if err := readJSON(r, &req); err != nil {
			utils.Logger.Warnf("Create task: invalid request body from %s", r.RemoteAddr)
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Script == "" || req.AgentID == "" {
			utils.Logger.Warnf("Create task: missing script or agent_id from %s", r.RemoteAddr)
			http.Error(w, "script and agent_id are required", http.StatusBadRequest)
			return
		}
		task := &models.Task{
			ID:        newID(),
			Script:    req.Script,
			Config:    req.Config,
			Status:    models.TaskStatusPending,
			AgentID:   req.AgentID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.signTask(task); err != nil {
			utils.Logger.Errorf("Sign task: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		s.store.createTask(task)
		s.hub.push(task.AgentID, task)
		utils.Logger.Infof("Task %s created: script=%s agent=%s", task.ID, task.Script, task.AgentID)
		writeJSON(w, http.StatusCreated, map[string]string{"id": task.ID})

	default:
		methodNotAllowed(w, r)
	}
}

// handleTask serves GET /api/v1/tasks/{id}.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}
	id := r.PathValue("id")
	task, ok := s.store.getTask(id)
	if !ok {
		utils.Logger.Warnf("Task %s not found", id)
		http.Error(w, fmt.Sprintf("task %s not found", id), http.StatusNotFound)
		return
	}
	utils.Logger.Debugf("Get task %s (status: %s)", id, task.Status)
	writeJSON(w, http.StatusOK, task)
}
