package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"raspideploy/internal/models"
	"raspideploy/internal/utils"
)

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

// ---- /health ---------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// ---- /api/v1/agents --------------------------------------------------------

// handleAgents serves GET /api/v1/agents — list all registered agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.store.listAgents())
}

// handleHeartbeat serves POST /api/v1/agents/heartbeat.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req models.HeartbeatRequest
	if err := readJSON(r, &req); err != nil || req.AgentID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.store.upsertAgent(&models.Agent{
		ID:            req.AgentID,
		Hostname:      req.Hostname,
		IPAddress:     req.IPAddress,
		Version:       req.Version,
		Status:        "online",
		LastHeartbeat: time.Now(),
		Metadata:      req.Metadata,
	})
	utils.Logger.Debugf("heartbeat from %s (%s)", req.AgentID, req.Hostname)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAgentTasks serves GET /api/v1/agents/{id}/tasks — pending tasks for one agent.
func (s *Server) handleAgentTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agentID := r.PathValue("id")
	tasks := s.store.listTasks(agentID, string(models.TaskStatusPending))
	utils.Logger.Debugf("serving %d pending task(s) to agent %s", len(tasks), agentID)
	writeJSON(w, http.StatusOK, tasks)
}

// ---- /api/v1/tasks ---------------------------------------------------------

// handleTasks serves GET and POST /api/v1/tasks.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentID := r.URL.Query().Get("agent_id")
		status := r.URL.Query().Get("status")
		writeJSON(w, http.StatusOK, s.store.listTasks(agentID, status))

	case http.MethodPost:
		var req models.CreateTaskRequest
		if err := readJSON(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Type == "" || req.AgentID == "" {
			http.Error(w, "type and agent_id are required", http.StatusBadRequest)
			return
		}
		task := &models.Task{
			ID:        newID(),
			Type:      req.Type,
			Status:    models.TaskStatusPending,
			AgentID:   req.AgentID,
			Payload:   req.Payload,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		s.store.createTask(task)
		utils.Logger.Infof("task %s created for agent %s (type: %s)", task.ID, task.AgentID, task.Type)
		writeJSON(w, http.StatusCreated, map[string]string{"id": task.ID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTask serves GET /api/v1/tasks/{id}.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	task, ok := s.store.getTask(id)
	if !ok {
		http.Error(w, fmt.Sprintf("task %s not found", id), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleTaskResult serves POST /api/v1/tasks/{id}/result — agent progress/completion reports.
func (s *Server) handleTaskResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	var req models.TaskResultRequest
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.store.updateTask(id, req.Status, req.Output, req.Error)
	utils.Logger.Infof("task %s → %s (agent: %s, duration: %dms)", id, req.Status, req.AgentID, req.DurationMs)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
