package server_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"raspicd/internal/models"
	"raspicd/internal/server"
)

const (
	testCISecret    = "ci-test-secret"
	testAgentSecret = "agent-test-secret"
)

func newTestServer(t *testing.T) (*server.Server, *httptest.Server) {
	t.Helper()
	_, sigKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	staticFS := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	srv := server.New(":0", testCISecret, testAgentSecret, "vtest", "", 90*time.Second, staticFS, sigKey)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func ciReq(t *testing.T, method, url string, body interface{}) *http.Request {
	t.Helper()
	var br io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		br = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, url, br)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testCISecret)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

// ---- /health ----------------------------------------------------------------

func TestHandleHealth(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "healthy" {
		t.Errorf("body status: got %q, want healthy", result["status"])
	}
}

func TestHandleHealthMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

// ---- X-RasPiCD-Version header -----------------------------------------------

func TestVersionHeader(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if v := resp.Header.Get("X-RasPiCD-Version"); v != "vtest" {
		t.Errorf("version header: got %q, want vtest", v)
	}
}

// ---- /api/v1/pubkey ---------------------------------------------------------

func TestHandlePubKey(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/pubkey")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result["public_key"]) != 64 {
		t.Errorf("public_key hex length: got %d, want 64", len(result["public_key"]))
	}
}

// ---- auth middleware --------------------------------------------------------

func TestAuthUnauthorizedNoHeader(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

func TestAuthUnauthorizedWrongToken(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

// ---- /api/v1/agents ---------------------------------------------------------

func TestHandleAgentsEmpty(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodGet, ts.URL+"/api/v1/agents", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var agents []*models.Agent
	json.NewDecoder(resp.Body).Decode(&agents)
	if len(agents) != 0 {
		t.Errorf("expected empty agents list, got %d", len(agents))
	}
}

func TestHandleAgentsMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/agents", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

// ---- /api/v1/tasks ----------------------------------------------------------

func TestHandleCreateTask(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
		AgentID: "pi1",
		Script:  "deploy",
	})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status: got %d, want 201", resp.StatusCode)
	}
	var created map[string]string
	json.NewDecoder(resp.Body).Decode(&created)
	if created["id"] == "" {
		t.Error("no task ID returned")
	}
}

func TestHandleCreateTaskMissingScript(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{AgentID: "pi1"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateTaskMissingAgentID(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{Script: "deploy"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestHandleGetTask(t *testing.T) {
	_, ts := newTestServer(t)

	// Create a task first
	createReq := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
		AgentID: "pi1",
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

	// Fetch it
	getReq := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks/"+taskID, nil)
	resp2, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp2.StatusCode)
	}
	var task models.Task
	json.NewDecoder(resp2.Body).Decode(&task)
	if task.Script != "deploy" {
		t.Errorf("script: got %q, want deploy", task.Script)
	}
	if task.Status != models.TaskStatusPending {
		t.Errorf("status: got %q, want pending", task.Status)
	}
	if task.Signature == "" {
		t.Error("task should be signed by the server")
	}
}

func TestHandleGetTaskNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks/noexist", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestHandleListTasks(t *testing.T) {
	_, ts := newTestServer(t)

	for _, agentID := range []string{"a1", "a1", "a2"} {
		req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks", models.CreateTaskRequest{
			AgentID: agentID,
			Script:  "deploy",
		})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// All tasks
	req := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var all []*models.Task
	json.NewDecoder(resp.Body).Decode(&all)
	if len(all) != 3 {
		t.Errorf("all tasks: got %d, want 3", len(all))
	}

	// Filter by agent
	req2 := ciReq(t, http.MethodGet, ts.URL+"/api/v1/tasks?agent_id=a1", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var byAgent []*models.Task
	json.NewDecoder(resp2.Body).Decode(&byAgent)
	if len(byAgent) != 2 {
		t.Errorf("a1 tasks: got %d, want 2", len(byAgent))
	}
}

func TestHandleTasksMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodDelete, ts.URL+"/api/v1/tasks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

// ---- /api/v1/tasks/broadcast ------------------------------------------------

func TestHandleBroadcastNoOnlineAgents(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks/broadcast", models.BroadcastTaskRequest{Script: "deploy"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409 (no online agents)", resp.StatusCode)
	}
}

func TestHandleBroadcastMissingScript(t *testing.T) {
	_, ts := newTestServer(t)
	req := ciReq(t, http.MethodPost, ts.URL+"/api/v1/tasks/broadcast", models.BroadcastTaskRequest{})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

// ---- 404 for unknown paths --------------------------------------------------

func TestHandleNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/no-such-path")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}
