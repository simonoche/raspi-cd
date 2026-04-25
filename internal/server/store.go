package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)

type store interface {
	upsertAgent(agent *models.Agent)
	getAgent(id string) (*models.Agent, bool)
	listAgents() []*models.Agent
	setAgentOffline(id string) bool
	// markStaleAgents sets status="offline" for agents whose last heartbeat
	// is older than threshold. Returns the IDs of newly-offline agents.
	markStaleAgents(threshold time.Duration) []string

	createTask(task *models.Task)
	getTask(id string) (*models.Task, bool)
	listTasks(agentID, status string) []*models.Task
	updateTask(id string, status models.TaskStatus, output, errMsg string)
}

type memStore struct {
	mu       sync.RWMutex
	agents   map[string]*models.Agent
	tasks    map[string]*models.Task
	filePath string // empty = no persistence
}

// storeData is the JSON schema written to disk.
type storeData struct {
	Agents map[string]*models.Agent `json:"agents"`
	Tasks  map[string]*models.Task  `json:"tasks"`
}

func newMemStore(filePath string) store {
	s := &memStore{
		agents:   make(map[string]*models.Agent),
		tasks:    make(map[string]*models.Task),
		filePath: filePath,
	}
	s.load()
	return s
}

// load reads the JSON file into memory. Silently does nothing if the file
// does not exist yet (fresh start).
func (s *memStore) load() {
	if s.filePath == "" {
		return
	}
	b, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		utils.Logger.Errorf("Load store from %s: %v", s.filePath, err)
		return
	}
	var data storeData
	if err := json.Unmarshal(b, &data); err != nil {
		utils.Logger.Errorf("Load store: unmarshal: %v", err)
		return
	}
	if data.Agents != nil {
		s.agents = data.Agents
	}
	if data.Tasks != nil {
		s.tasks = data.Tasks
	}
	utils.Logger.Infof("Loaded %d agent(s) and %d task(s) from %s", len(s.agents), len(s.tasks), s.filePath)
}

// persist writes the store to disk atomically (write to .tmp, then rename).
// Must be called with s.mu held.
func (s *memStore) persist() {
	if s.filePath == "" {
		return
	}
	data := storeData{Agents: s.agents, Tasks: s.tasks}
	b, err := json.Marshal(data)
	if err != nil {
		utils.Logger.Errorf("Persist store: marshal: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0700); err != nil {
		utils.Logger.Errorf("Persist store: mkdir: %v", err)
		return
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		utils.Logger.Errorf("Persist store: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		utils.Logger.Errorf("Persist store: rename: %v", err)
	}
}

func (s *memStore) upsertAgent(agent *models.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
	s.persist()
}

func (s *memStore) getAgent(id string) (*models.Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	return a, ok
}

func (s *memStore) listAgents() []*models.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*models.Agent, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, a)
	}
	return out
}

func (s *memStore) setAgentOffline(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return false
	}
	a.Status = "offline"
	s.persist()
	return true
}

func (s *memStore) markStaleAgents(threshold time.Duration) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-threshold)
	var offline []string
	for _, a := range s.agents {
		if a.Status == "online" && a.LastHeartbeat.Before(cutoff) {
			a.Status = "offline"
			offline = append(offline, a.ID)
		}
	}
	if len(offline) > 0 {
		s.persist()
	}
	return offline
}

func (s *memStore) createTask(task *models.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
	s.persist()
}

func (s *memStore) getTask(id string) (*models.Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}

func (s *memStore) listTasks(agentID, status string) []*models.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*models.Task, 0)
	for _, t := range s.tasks {
		if agentID != "" && t.AgentID != agentID {
			continue
		}
		if status != "" && string(t.Status) != status {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (s *memStore) updateTask(id string, status models.TaskStatus, output, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return
	}
	t.Status = status
	if output != "" {
		t.Output = output
	}
	if errMsg != "" {
		t.Error = errMsg
	}
	t.UpdatedAt = time.Now()
	s.persist()
}
