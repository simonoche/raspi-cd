package models

import "time"

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task is a unit of work queued for a specific agent.
type Task struct {
	ID        string                 `json:"id"`
	Script    string                 `json:"script"`
	Config    map[string]interface{} `json:"config,omitempty"`
	Status    TaskStatus             `json:"status"`
	AgentID   string                 `json:"agent_id"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// Agent represents a registered Raspberry Pi running the agent daemon.
type Agent struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	IPAddress     string            `json:"ip_address,omitempty"`
	Version       string            `json:"version"`
	Status        string            `json:"status"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// HeartbeatRequest is sent by agents to register/refresh themselves.
type HeartbeatRequest struct {
	AgentID   string            `json:"agent_id"`
	Hostname  string            `json:"hostname"`
	IPAddress string            `json:"ip_address,omitempty"`
	Version   string            `json:"version"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// CreateTaskRequest is the body of POST /api/v1/tasks.
type CreateTaskRequest struct {
	AgentID string                 `json:"agent_id"`
	Script  string                 `json:"script"`
	Config  map[string]interface{} `json:"config,omitempty"`
}

// BroadcastTaskRequest is the body of POST /api/v1/tasks/broadcast.
// It creates one task per online agent with the same script and config.
type BroadcastTaskRequest struct {
	Script string                 `json:"script"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// BroadcastTaskItem is one entry in the response of POST /api/v1/tasks/broadcast.
type BroadcastTaskItem struct {
	AgentID string `json:"agent_id"`
	TaskID  string `json:"task_id"`
}

// TaskResultRequest is the body of POST /api/v1/tasks/{id}/result.
type TaskResultRequest struct {
	AgentID    string     `json:"agent_id"`
	Status     TaskStatus `json:"status"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
}
