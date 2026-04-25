package models

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
