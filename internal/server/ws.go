package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)

const wsMaxMsgSize = 64 * 1024 // 64 KB

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Agents connect from varied origins; origin checking is handled by the
	// Bearer token on the Authorization header (checked before upgrade).
	CheckOrigin: func(*http.Request) bool { return true },
}

// readHello reads and validates the first frame sent by an agent after connecting.
// The agent must send a WSMsgHello with a non-empty AgentID within WSPongWait.
func readHello(wsc *websocket.Conn) (*models.WSMessage, error) {
	wsc.SetReadDeadline(time.Now().Add(models.WSPongWait))
	_, raw, err := wsc.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var hello models.WSMessage
	if err := json.Unmarshal(raw, &hello); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if hello.Type != models.WSMsgHello || hello.AgentID == "" {
		return nil, fmt.Errorf("expected hello with agent_id, got type=%q", hello.Type)
	}
	return &hello, nil
}

// handleAgentWS manages GET /api/v1/agents/ws — the persistent WebSocket
// connection each agent maintains.
//
// Protocol:
//  1. Agent connects and immediately sends a WSMsgHello frame.
//  2. Server registers the agent and flushes any tasks queued while it was offline.
//  3. Server pushes WSMsgTask frames as new tasks arrive.
//  4. Agent sends WSMsgResult frames as tasks progress/complete.
//  5. On disconnect the agent is marked offline; tasks remain pending in the store.
func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	wsc, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		utils.Logger.Warnf("WS upgrade from %s: %v", r.RemoteAddr, err)
		return
	}

	wsc.SetReadLimit(wsMaxMsgSize)

	hello, err := readHello(wsc)
	if err != nil {
		utils.Logger.Warnf("WS: hello failed from %s: %v", r.RemoteAddr, err)
		wsc.Close()
		return
	}

	agentID := hello.AgentID

	// Reset read deadline to the pong-based keepalive.
	wsc.SetReadDeadline(time.Now().Add(models.WSPongWait))
	wsc.SetPongHandler(func(string) error {
		wsc.SetReadDeadline(time.Now().Add(models.WSPongWait))
		s.store.touchAgent(agentID)
		return nil
	})

	// Check if this agent ID is already connected
	if s.hub.isConnected(agentID) {
		utils.Logger.Warnf("Duplicate connection attempt: agent %s is already connected. Rejecting new connection from %s", agentID, r.RemoteAddr)
		errMsg := models.WSMessage{
			Type:   models.WSMsgError,
			Reason: "Agent ID already connected. Only one connection per agent ID is allowed.",
		}
		errBytes, _ := json.Marshal(errMsg)
		wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
		wsc.WriteMessage(websocket.TextMessage, errBytes) //nolint:errcheck
		wsc.Close()
		return
	}

	existing, exists := s.store.getAgent(agentID)
	s.store.upsertAgent(&models.Agent{
		ID:            agentID,
		Hostname:      hello.Hostname,
		IPAddress:     hello.IPAddress,
		Version:       hello.Version,
		Status:        "online",
		LastHeartbeat: time.Now(),
		Metadata:      hello.Metadata,
	})
	switch {
	case !exists:
		utils.Logger.Infof("Agent connected: %s (%s) %s", agentID, hello.Hostname, hello.Version)
	case existing.Status == "offline":
		utils.Logger.Infof("Agent reconnected: %s (%s)", agentID, hello.Hostname)
	default:
		utils.Logger.Debugf("Agent re-registered: %s", agentID)
	}

	c := &agentConn{send: make(chan []byte, 32)}
	s.hub.register(agentID, c)

	// Flush tasks that were created while the agent was offline.
	pending := s.store.listTasks(agentID, string(models.TaskStatusPending))
	for _, task := range pending {
		if b, err := json.Marshal(models.WSMessage{Type: models.WSMsgTask, Task: task}); err == nil {
			c.send <- b
		}
	}
	if len(pending) > 0 {
		utils.Logger.Infof("Flushing %d pending task(s) to agent %s", len(pending), agentID)
	}

	go s.wsWriteLoop(wsc, c, agentID)
	s.wsReadLoop(wsc, c, agentID)
}

// wsWriteLoop drains c.send and writes frames. Sends a WebSocket ping every
// models.WSPingPeriod so dead connections are detected within models.WSPongWait.
func (s *Server) wsWriteLoop(wsc *websocket.Conn, c *agentConn, agentID string) {
	ticker := time.NewTicker(models.WSPingPeriod)
	defer func() {
		ticker.Stop()
		wsc.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
			if !ok {
				// Channel closed because a newer connection took over.
				wsc.WriteMessage(websocket.CloseMessage, []byte{}) //nolint:errcheck
				return
			}
			if err := wsc.WriteMessage(websocket.TextMessage, msg); err != nil {
				utils.Logger.Warnf("WS write error for agent %s: %v", agentID, err)
				return
			}
		case <-ticker.C:
			wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
			if err := wsc.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// wsReadLoop reads frames from the agent until the connection closes, then
// marks the agent offline.
func (s *Server) wsReadLoop(wsc *websocket.Conn, c *agentConn, agentID string) {
	defer func() {
		s.hub.unregister(agentID, c)
		s.store.setAgentOffline(agentID)
		utils.Logger.Infof("Agent disconnected: %s", agentID)
		wsc.Close()
	}()

	for {
		_, raw, err := wsc.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				utils.Logger.Warnf("WS unexpected close from agent %s: %v", agentID, err)
			}
			return
		}

		var msg models.WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			utils.Logger.Warnf("WS: unparseable message from agent %s: %v", agentID, err)
			continue
		}

		if msg.Type == models.WSMsgResult {
			s.handleWSResult(agentID, &msg)
		} else {
			utils.Logger.Warnf("WS: unexpected message type %q from agent %s", msg.Type, agentID)
		}
	}
}

func (s *Server) handleWSResult(agentID string, msg *models.WSMessage) {
	s.store.updateTask(msg.TaskID, msg.Status, msg.Output, msg.Error)
	s.store.touchAgent(agentID)
	switch msg.Status {
	case models.TaskStatusRunning:
		utils.Logger.Infof("Task %s running (agent: %s)", msg.TaskID, agentID)
	case models.TaskStatusCompleted:
		utils.Logger.Infof("Task %s completed in %dms (agent: %s)", msg.TaskID, msg.DurationMs, agentID)
	case models.TaskStatusFailed:
		utils.Logger.Warnf("Task %s failed in %dms (agent: %s): %s", msg.TaskID, msg.DurationMs, agentID, msg.Error)
	default:
		utils.Logger.Infof("Task %s → %s (agent: %s)", msg.TaskID, msg.Status, agentID)
	}
}
