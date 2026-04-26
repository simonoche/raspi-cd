package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"raspicd/internal/models"
)

func newTestStore(t *testing.T) store {
	t.Helper()
	return newMemStore("")
}

func newTestStoreWithFile(t *testing.T) (store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.json")
	return newMemStore(path), path
}

// ---- Agent tests ------------------------------------------------------------

func TestUpsertAndGetAgent(t *testing.T) {
	s := newTestStore(t)
	a := &models.Agent{ID: "pi1", Hostname: "pi1.local", Status: "online", LastHeartbeat: time.Now()}
	s.upsertAgent(a)
	got, ok := s.getAgent("pi1")
	if !ok {
		t.Fatal("agent not found after upsert")
	}
	if got.Hostname != "pi1.local" {
		t.Errorf("hostname: got %q, want %q", got.Hostname, "pi1.local")
	}
}

func TestGetAgentMissing(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.getAgent("noexist")
	if ok {
		t.Error("expected agent not found, got ok=true")
	}
}

func TestUpsertAgentOverwrites(t *testing.T) {
	s := newTestStore(t)
	s.upsertAgent(&models.Agent{ID: "pi1", Hostname: "old"})
	s.upsertAgent(&models.Agent{ID: "pi1", Hostname: "new"})
	got, _ := s.getAgent("pi1")
	if got.Hostname != "new" {
		t.Errorf("upsert should overwrite: got %q, want %q", got.Hostname, "new")
	}
}

func TestListAgents(t *testing.T) {
	s := newTestStore(t)
	if agents := s.listAgents(); len(agents) != 0 {
		t.Errorf("empty store: got %d agents, want 0", len(agents))
	}
	s.upsertAgent(&models.Agent{ID: "a1"})
	s.upsertAgent(&models.Agent{ID: "a2"})
	if agents := s.listAgents(); len(agents) != 2 {
		t.Errorf("got %d agents, want 2", len(agents))
	}
}

func TestSetAgentOffline(t *testing.T) {
	s := newTestStore(t)
	s.upsertAgent(&models.Agent{ID: "pi1", Status: "online"})
	if !s.setAgentOffline("pi1") {
		t.Fatal("setAgentOffline returned false for existing agent")
	}
	a, _ := s.getAgent("pi1")
	if a.Status != "offline" {
		t.Errorf("status: got %q, want offline", a.Status)
	}
}

func TestSetAgentOfflineMissing(t *testing.T) {
	s := newTestStore(t)
	if s.setAgentOffline("noexist") {
		t.Error("setAgentOffline should return false for unknown agent")
	}
}

func TestMarkStaleAgents(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().Add(-10 * time.Minute)
	s.upsertAgent(&models.Agent{ID: "stale", Status: "online", LastHeartbeat: old})
	s.upsertAgent(&models.Agent{ID: "fresh", Status: "online", LastHeartbeat: time.Now()})
	s.upsertAgent(&models.Agent{ID: "already-offline", Status: "offline", LastHeartbeat: old})

	stale := s.markStaleAgents(5 * time.Minute)
	if len(stale) != 1 || stale[0] != "stale" {
		t.Errorf("markStaleAgents: got %v, want [stale]", stale)
	}

	a, _ := s.getAgent("stale")
	if a.Status != "offline" {
		t.Errorf("stale agent: got %q, want offline", a.Status)
	}
	fresh, _ := s.getAgent("fresh")
	if fresh.Status != "online" {
		t.Errorf("fresh agent: got %q, want online", fresh.Status)
	}
	ao, _ := s.getAgent("already-offline")
	if ao.Status != "offline" {
		t.Errorf("already-offline agent should stay offline")
	}
}

func TestMarkStaleAgentsNoneStale(t *testing.T) {
	s := newTestStore(t)
	s.upsertAgent(&models.Agent{ID: "fresh", Status: "online", LastHeartbeat: time.Now()})
	stale := s.markStaleAgents(5 * time.Minute)
	if len(stale) != 0 {
		t.Errorf("expected no stale agents, got %v", stale)
	}
}

func TestTouchAgent(t *testing.T) {
	s := newTestStore(t)
	past := time.Now().Add(-10 * time.Minute)
	s.upsertAgent(&models.Agent{ID: "pi1", LastHeartbeat: past})

	if !s.touchAgent("pi1") {
		t.Fatal("touchAgent returned false for existing agent")
	}
	a, _ := s.getAgent("pi1")
	if !a.LastHeartbeat.After(past) {
		t.Error("touchAgent did not update LastHeartbeat")
	}
}

func TestTouchAgentMissing(t *testing.T) {
	s := newTestStore(t)
	if s.touchAgent("noexist") {
		t.Error("touchAgent should return false for unknown agent")
	}
}

func TestTouchAgentDoesNotPersist(t *testing.T) {
	s, path := newTestStoreWithFile(t)
	s.upsertAgent(&models.Agent{ID: "pi1", Status: "online"})

	// Modify file mtime so we can detect if persist was called
	info1, _ := os.Stat(path)

	// Small sleep to ensure mtime would differ if written
	time.Sleep(10 * time.Millisecond)
	s.touchAgent("pi1")

	info2, _ := os.Stat(path)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("touchAgent should not persist to disk")
	}
}

// ---- Task tests -------------------------------------------------------------

func TestCreateAndGetTask(t *testing.T) {
	s := newTestStore(t)
	task := &models.Task{ID: "t1", Script: "deploy", AgentID: "pi1", Status: models.TaskStatusPending}
	s.createTask(task)
	got, ok := s.getTask("t1")
	if !ok {
		t.Fatal("task not found after create")
	}
	if got.Script != "deploy" {
		t.Errorf("script: got %q, want deploy", got.Script)
	}
}

func TestGetTaskMissing(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.getTask("noexist")
	if ok {
		t.Error("expected task not found, got ok=true")
	}
}

func TestListTasksAll(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", AgentID: "a1", Status: models.TaskStatusPending})
	s.createTask(&models.Task{ID: "t2", AgentID: "a1", Status: models.TaskStatusCompleted})
	s.createTask(&models.Task{ID: "t3", AgentID: "a2", Status: models.TaskStatusPending})

	if tasks := s.listTasks("", ""); len(tasks) != 3 {
		t.Errorf("listTasks('',''): got %d, want 3", len(tasks))
	}
}

func TestListTasksByAgent(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", AgentID: "a1", Status: models.TaskStatusPending})
	s.createTask(&models.Task{ID: "t2", AgentID: "a1", Status: models.TaskStatusCompleted})
	s.createTask(&models.Task{ID: "t3", AgentID: "a2", Status: models.TaskStatusPending})

	if tasks := s.listTasks("a1", ""); len(tasks) != 2 {
		t.Errorf("listTasks(a1,''): got %d, want 2", len(tasks))
	}
	if tasks := s.listTasks("a2", ""); len(tasks) != 1 {
		t.Errorf("listTasks(a2,''): got %d, want 1", len(tasks))
	}
}

func TestListTasksByStatus(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", AgentID: "a1", Status: models.TaskStatusPending})
	s.createTask(&models.Task{ID: "t2", AgentID: "a1", Status: models.TaskStatusCompleted})
	s.createTask(&models.Task{ID: "t3", AgentID: "a2", Status: models.TaskStatusPending})

	if tasks := s.listTasks("", string(models.TaskStatusPending)); len(tasks) != 2 {
		t.Errorf("listTasks('',pending): got %d, want 2", len(tasks))
	}
}

func TestListTasksByAgentAndStatus(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", AgentID: "a1", Status: models.TaskStatusPending})
	s.createTask(&models.Task{ID: "t2", AgentID: "a1", Status: models.TaskStatusCompleted})
	s.createTask(&models.Task{ID: "t3", AgentID: "a2", Status: models.TaskStatusPending})

	if tasks := s.listTasks("a1", string(models.TaskStatusPending)); len(tasks) != 1 {
		t.Errorf("listTasks(a1,pending): got %d, want 1", len(tasks))
	}
}

func TestUpdateTask(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", Status: models.TaskStatusPending})
	s.updateTask("t1", models.TaskStatusCompleted, "output text", "")

	got, _ := s.getTask("t1")
	if got.Status != models.TaskStatusCompleted {
		t.Errorf("status: got %q, want completed", got.Status)
	}
	if got.Output != "output text" {
		t.Errorf("output: got %q, want %q", got.Output, "output text")
	}
	if got.Error != "" {
		t.Errorf("error: got %q, want empty", got.Error)
	}
}

func TestUpdateTaskError(t *testing.T) {
	s := newTestStore(t)
	s.createTask(&models.Task{ID: "t1", Status: models.TaskStatusPending})
	s.updateTask("t1", models.TaskStatusFailed, "", "something went wrong")

	got, _ := s.getTask("t1")
	if got.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("error: got %q", got.Error)
	}
}

func TestUpdateTaskMissingNoOp(t *testing.T) {
	s := newTestStore(t)
	s.updateTask("noexist", models.TaskStatusCompleted, "out", "") // must not panic
}

// ---- Persistence tests ------------------------------------------------------

func TestPersistenceRoundTrip(t *testing.T) {
	st, path := newTestStoreWithFile(t)
	st.upsertAgent(&models.Agent{ID: "pi1", Hostname: "pi1.local", Status: "online"})
	st.createTask(&models.Task{ID: "t1", Script: "deploy", AgentID: "pi1", Status: models.TaskStatusPending})

	st2 := newMemStore(path)
	a, ok := st2.getAgent("pi1")
	if !ok {
		t.Fatal("agent not persisted")
	}
	if a.Hostname != "pi1.local" {
		t.Errorf("hostname: got %q, want pi1.local", a.Hostname)
	}
	task, ok := st2.getTask("t1")
	if !ok {
		t.Fatal("task not persisted")
	}
	if task.Script != "deploy" {
		t.Errorf("script: got %q, want deploy", task.Script)
	}
}

func TestPersistenceAtomicRename(t *testing.T) {
	st, path := newTestStoreWithFile(t)
	st.upsertAgent(&models.Agent{ID: "pi1"})

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file should not exist after successful persist")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("store file must exist after persist: %v", err)
	}
}

func TestPersistenceNoFileOnEmptyPath(t *testing.T) {
	s := newTestStore(t) // no file path
	s.upsertAgent(&models.Agent{ID: "pi1"})
	// Nothing to verify — just ensure no panic and no file created in cwd
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "store.json")
	s := newMemStore(path) // should not panic; MkdirAll happens at persist time
	_ = s
}
