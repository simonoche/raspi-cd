package server

import (
	"sync"
	"time"

	"raspideploy/internal/models"
)

type store interface {
	upsertAgent(agent *models.Agent)
	listAgents() []*models.Agent

	createTask(task *models.Task)
	getTask(id string) (*models.Task, bool)
	listTasks(agentID, status string) []*models.Task
	updateTask(id string, status models.TaskStatus, output, errMsg string)
}

type memStore struct {
	mu     sync.RWMutex
	agents map[string]*models.Agent
	tasks  map[string]*models.Task
}

func newMemStore() store {
	return &memStore{
		agents: make(map[string]*models.Agent),
		tasks:  make(map[string]*models.Task),
	}
}

func (s *memStore) upsertAgent(agent *models.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID] = agent
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

func (s *memStore) createTask(task *models.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
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
}
