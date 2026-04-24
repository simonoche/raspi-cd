package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"raspideploy/internal/models"
	"raspideploy/internal/utils"
)

// Client communicates with the RaspiDeploy server.
type Client struct {
	serverURL  string
	agentID    string
	secret     string
	httpClient *http.Client
}

// NewClient creates a Client.
func NewClient(serverURL, agentID, secret string) *Client {
	return &Client{
		serverURL:  serverURL,
		agentID:    agentID,
		secret:     secret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// do builds and executes an authenticated HTTP request.
func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewBuffer(b)
	} else {
		bodyReader = &bytes.Buffer{}
	}

	req, err := http.NewRequest(method, c.serverURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	return c.httpClient.Do(req)
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("server %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// SendHeartbeat registers or refreshes this agent on the server.
func (c *Client) SendHeartbeat(hostname, version string) error {
	resp, err := c.do(http.MethodPost, "/api/v1/agents/heartbeat", models.HeartbeatRequest{
		AgentID:  c.agentID,
		Hostname: hostname,
		Version:  version,
	})
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	utils.Logger.Debugf("heartbeat sent")
	return nil
}

// FetchTasks retrieves pending tasks assigned to this agent.
func (c *Client) FetchTasks() ([]*models.Task, error) {
	resp, err := c.do(http.MethodGet, "/api/v1/agents/"+c.agentID+"/tasks", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch tasks: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, fmt.Errorf("fetch tasks: %w", err)
	}
	var tasks []*models.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("decode tasks: %w", err)
	}
	utils.Logger.Debugf("fetched %d task(s)", len(tasks))
	return tasks, nil
}

// Disconnect notifies the server that this agent is going offline.
func (c *Client) Disconnect() error {
	resp, err := c.do(http.MethodPost, "/api/v1/agents/"+c.agentID+"/disconnect", nil)
	if err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	return nil
}

// ReportResult sends a task status update or final result to the server.
func (c *Client) ReportResult(taskID string, result models.TaskResultRequest) error {
	resp, err := c.do(http.MethodPost, "/api/v1/tasks/"+taskID+"/result", result)
	if err != nil {
		return fmt.Errorf("report result: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return fmt.Errorf("report result: %w", err)
	}
	utils.Logger.Debugf("reported %s for task %s", result.Status, taskID)
	return nil
}
