package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"raspicd/internal/models"
)

// wsURL converts an httptest server URL (http://...) to a WebSocket URL (ws://...).
func wsURL(tsURL string) string {
	return "ws" + strings.TrimPrefix(tsURL, "http") + "/api/v1/agents/ws"
}

// dialAgent opens an authenticated WebSocket connection to the test server.
func dialAgent(t *testing.T, tsURL string) *websocket.Conn {
	t.Helper()
	header := http.Header{"Authorization": {"Bearer " + testAgentSecret}}
	wsc, resp, err := websocket.DefaultDialer.Dial(wsURL(tsURL), header)
	if err != nil {
		t.Fatalf("WS dial: %v (HTTP status %v)", err, resp)
	}
	t.Cleanup(func() { wsc.Close() })
	return wsc
}

// sendHello sends a WSMsgHello to the server.
func sendHello(t *testing.T, wsc *websocket.Conn, agentID, hostname string) {
	t.Helper()
	msg := models.WSMessage{
		Type:     models.WSMsgHello,
		AgentID:  agentID,
		Hostname: hostname,
		Version:  "test",
	}
	if err := wsc.WriteJSON(msg); err != nil {
		t.Fatalf("sendHello: %v", err)
	}
}

// waitFor polls check at 20ms intervals until it returns true or 2s elapses.
func waitFor(t *testing.T, label string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor(%s): condition not met within 2s", label)
}

// agentStatus fetches /api/v1/agents and returns the status of agentID,
// or "" if the agent is not found.
func agentStatus(t *testing.T, tsURL, agentID string) string {
	t.Helper()
	req := ciReq(t, http.MethodGet, tsURL+"/api/v1/agents", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var agents []*models.Agent
	json.NewDecoder(resp.Body).Decode(&agents)
	for _, a := range agents {
		if a.ID == agentID {
			return a.Status
		}
	}
	return ""
}

// ---- Tests ------------------------------------------------------------------

func TestWSUnauthorized(t *testing.T) {
	_, ts := newTestServer(t)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(ts.URL), nil)
	if err == nil {
		t.Fatal("expected dial to fail without auth header")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("HTTP status: got %d, want 401", resp.StatusCode)
	}
}

func TestWSConnectAgentGoesOnline(t *testing.T) {
	_, ts := newTestServer(t)
	wsc := dialAgent(t, ts.URL)
	sendHello(t, wsc, "pi-test", "pi.local")

	waitFor(t, "agent online", func() bool {
		return agentStatus(t, ts.URL, "pi-test") == "online"
	})
}

func TestWSDisconnectAgentGoesOffline(t *testing.T) {
	_, ts := newTestServer(t)
	wsc := dialAgent(t, ts.URL)
	sendHello(t, wsc, "pi-test", "pi.local")

	waitFor(t, "agent online", func() bool {
		return agentStatus(t, ts.URL, "pi-test") == "online"
	})

	// Close the connection from the client side.
	wsc.Close()

	waitFor(t, "agent offline", func() bool {
		return agentStatus(t, ts.URL, "pi-test") == "offline"
	})
}

func TestWSPendingTaskFlushedOnConnect(t *testing.T) {
	_, ts := newTestServer(t)

	// Create a task before the agent connects.
	createReq := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
		AgentID: "pi-flush",
		Script:  "deploy",
	})
	resp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Now connect the agent.
	wsc := dialAgent(t, ts.URL)
	sendHello(t, wsc, "pi-flush", "pi.local")

	// The server should flush the pending task immediately.
	wsc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := wsc.ReadMessage()
	if err != nil {
		t.Fatalf("expected pending task, got read error: %v", err)
	}
	var msg models.WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != models.WSMsgTask {
		t.Errorf("msg.Type: got %q, want %q", msg.Type, models.WSMsgTask)
	}
	if msg.Task == nil || msg.Task.Script != "deploy" {
		t.Errorf("unexpected task: %+v", msg.Task)
	}
}

func TestWSLiveTaskDelivered(t *testing.T) {
	_, ts := newTestServer(t)

	wsc := dialAgent(t, ts.URL)
	sendHello(t, wsc, "pi-live", "pi.local")

	waitFor(t, "agent online", func() bool {
		return agentStatus(t, ts.URL, "pi-live") == "online"
	})

	// Create a task now that the agent is connected.
	createReq := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
		AgentID: "pi-live",
		Script:  "restart",
	})
	resp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Agent should receive it over the open WebSocket.
	wsc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := wsc.ReadMessage()
	if err != nil {
		t.Fatalf("expected task message, got: %v", err)
	}
	var msg models.WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != models.WSMsgTask {
		t.Errorf("msg.Type: got %q, want %q", msg.Type, models.WSMsgTask)
	}
	if msg.Task == nil || msg.Task.Script != "restart" {
		t.Errorf("unexpected task: %+v", msg.Task)
	}
}

func TestWSResultUpdatesTask(t *testing.T) {
	_, ts := newTestServer(t)

	wsc := dialAgent(t, ts.URL)
	sendHello(t, wsc, "pi-result", "pi.local")

	waitFor(t, "agent online", func() bool {
		return agentStatus(t, ts.URL, "pi-result") == "online"
	})

	// Create a task.
	createReq := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
		AgentID: "pi-result",
		Script:  "deploy",
	})
	resp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]string
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	taskID := created["id"]

	// Receive the task from the server.
	wsc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := wsc.ReadMessage()
	if err != nil {
		t.Fatalf("expected task: %v", err)
	}
	var taskMsg models.WSMessage
	json.Unmarshal(raw, &taskMsg)

	// Send a "running" result back.
	runningResult := models.WSMessage{
		Type:   models.WSMsgResult,
		TaskID: taskID,
		Status: models.TaskStatusRunning,
	}
	if err := wsc.WriteJSON(runningResult); err != nil {
		t.Fatalf("send running result: %v", err)
	}

	waitFor(t, "task running", func() bool {
		req := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks/"+taskID, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var task models.Task
		json.NewDecoder(resp.Body).Decode(&task)
		return task.Status == models.TaskStatusRunning
	})

	// Send "completed" result.
	doneResult := models.WSMessage{
		Type:       models.WSMsgResult,
		TaskID:     taskID,
		Status:     models.TaskStatusCompleted,
		Output:     "all good",
		DurationMs: 42,
	}
	if err := wsc.WriteJSON(doneResult); err != nil {
		t.Fatalf("send done result: %v", err)
	}

	waitFor(t, "task completed", func() bool {
		req := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks/"+taskID, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var task models.Task
		json.NewDecoder(resp.Body).Decode(&task)
		return task.Status == models.TaskStatusCompleted && task.Output == "all good"
	})
}

func TestWSBroadcast(t *testing.T) {
	_, ts := newTestServer(t)

	// Connect two agents.
	wsc1 := dialAgent(t, ts.URL)
	sendHello(t, wsc1, "pi-b1", "pi-b1.local")

	wsc2 := dialAgent(t, ts.URL)
	sendHello(t, wsc2, "pi-b2", "pi-b2.local")

	waitFor(t, "both online", func() bool {
		return agentStatus(t, ts.URL, "pi-b1") == "online" &&
			agentStatus(t, ts.URL, "pi-b2") == "online"
	})

	// Broadcast a task.
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks/broadcast", models.BroadcastTaskRequest{Script: "update"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("broadcast status: got %d, want 201", resp.StatusCode)
	}
	var results []models.BroadcastTaskItem
	json.NewDecoder(resp.Body).Decode(&results)
	if len(results) != 2 {
		t.Errorf("broadcast created %d tasks, want 2", len(results))
	}

	// Both agents should receive a task.
	for i, wsc := range []*websocket.Conn{wsc1, wsc2} {
		wsc.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, err := wsc.ReadMessage()
		if err != nil {
			t.Fatalf("agent %d: expected task: %v", i+1, err)
		}
		var msg models.WSMessage
		json.Unmarshal(raw, &msg)
		if msg.Type != models.WSMsgTask {
			t.Errorf("agent %d msg type: got %q, want task", i+1, msg.Type)
		}
	}
}
