package server

import (
	"encoding/json"
	"testing"
	"time"

	"raspicd/internal/models"
)

func TestHubPushDelivered(t *testing.T) {
	h := newHub()
	c := &agentConn{send: make(chan []byte, 4)}
	h.register("pi1", c)

	task := &models.Task{ID: "t1", Script: "deploy"}
	if !h.push("pi1", task) {
		t.Fatal("push returned false for registered agent")
	}

	select {
	case raw := <-c.send:
		var msg models.WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != models.WSMsgTask {
			t.Errorf("msg.Type: got %q, want %q", msg.Type, models.WSMsgTask)
		}
		if msg.Task == nil || msg.Task.ID != "t1" {
			t.Error("task ID mismatch in delivered message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("nothing on send channel after push")
	}
}

func TestHubPushNoAgent(t *testing.T) {
	h := newHub()
	task := &models.Task{ID: "t1"}
	if h.push("noexist", task) {
		t.Error("push should return false for unregistered agent")
	}
}

func TestHubUnregister(t *testing.T) {
	h := newHub()
	c := &agentConn{send: make(chan []byte, 4)}
	h.register("pi1", c)
	h.unregister("pi1", c)

	if h.push("pi1", &models.Task{ID: "t1"}) {
		t.Error("push should return false after unregister")
	}
}

func TestHubUnregisterWrongConnIgnored(t *testing.T) {
	h := newHub()
	c1 := &agentConn{send: make(chan []byte, 4)}
	c2 := &agentConn{send: make(chan []byte, 4)}
	h.register("pi1", c1)
	h.register("pi1", c2) // c1 evicted

	// Unregistering the old conn must not remove c2
	h.unregister("pi1", c1)
	if !h.push("pi1", &models.Task{ID: "t1"}) {
		t.Error("c2 should still be registered after unregistering stale c1")
	}
}

func TestHubEvictionClosesOldChannel(t *testing.T) {
	h := newHub()
	c1 := &agentConn{send: make(chan []byte, 4)}
	h.register("pi1", c1)

	c2 := &agentConn{send: make(chan []byte, 4)}
	h.register("pi1", c2) // evicts c1

	select {
	case _, ok := <-c1.send:
		if ok {
			t.Error("c1.send should be closed (received value from supposedly closed channel)")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("c1.send was not closed after eviction")
	}
}

func TestHubPushFullBufferNonBlocking(t *testing.T) {
	h := newHub()
	c := &agentConn{send: make(chan []byte, 1)} // buffer of 1
	h.register("pi1", c)

	task := &models.Task{ID: "t1"}
	h.push("pi1", task) // fills the buffer

	// Second push: buffer full — must not block, must return false
	done := make(chan bool, 1)
	go func() { done <- h.push("pi1", task) }()
	select {
	case result := <-done:
		if result {
			t.Error("push should return false when send buffer is full")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("push blocked when buffer was full")
	}
}

func TestHubMultipleAgents(t *testing.T) {
	h := newHub()
	c1 := &agentConn{send: make(chan []byte, 4)}
	c2 := &agentConn{send: make(chan []byte, 4)}
	h.register("a1", c1)
	h.register("a2", c2)

	h.push("a1", &models.Task{ID: "t1"})
	h.push("a2", &models.Task{ID: "t2"})

	if len(c1.send) != 1 {
		t.Errorf("a1 channel: got %d messages, want 1", len(c1.send))
	}
	if len(c2.send) != 1 {
		t.Errorf("a2 channel: got %d messages, want 1", len(c2.send))
	}
}
