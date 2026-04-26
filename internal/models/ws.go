package models

import "time"

// WSWriteWait is the deadline for a single WebSocket write to complete.
// Used by both the server write-loop and the agent write goroutine.
const WSWriteWait = 10 * time.Second

// WSPingPeriod is how often the connection owner sends a ping frame.
// Must be strictly less than WSPongWait.
const WSPingPeriod = 30 * time.Second

// WSPongWait is the deadline for the remote side to respond with a pong.
// Must exceed WSPingPeriod so the connection is not torn down before the
// pong arrives.
const WSPongWait = 60 * time.Second

// WSMsgType identifies the WebSocket message direction and purpose.
type WSMsgType string

const (
	WSMsgHello  WSMsgType = "hello"  // agent → server: initial registration
	WSMsgTask   WSMsgType = "task"   // server → agent: task to execute
	WSMsgResult WSMsgType = "result" // agent → server: task result
)

// WSMessage is the JSON envelope for all messages on the persistent WebSocket
// connection between agent and server.
type WSMessage struct {
	Type WSMsgType `json:"type"`

	// hello (agent → server)
	AgentID   string            `json:"agent_id,omitempty"`
	Hostname  string            `json:"hostname,omitempty"`
	Version   string            `json:"version,omitempty"`
	IPAddress string            `json:"ip_address,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`

	// task (server → agent)
	Task *Task `json:"task,omitempty"`

	// result (agent → server)
	TaskID     string     `json:"task_id,omitempty"`
	Status     TaskStatus `json:"status,omitempty"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
}
