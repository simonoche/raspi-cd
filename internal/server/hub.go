package server

import (
	"encoding/json"
	"sync"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)

// agentConn holds the outbound send channel for one live agent connection.
type agentConn struct {
	send chan []byte
}

// hub tracks active WebSocket connections indexed by agent ID.
type hub struct {
	mu    sync.RWMutex
	conns map[string]*agentConn
}

func newHub() *hub {
	return &hub{conns: make(map[string]*agentConn)}
}

// register adds c as the live connection for agentID. Any previous connection
// is evicted by closing its send channel, which causes the write loop to exit.
func (h *hub) register(agentID string, c *agentConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.conns[agentID]; ok {
		close(prev.send)
	}
	h.conns[agentID] = c
}

// unregister removes the connection for agentID if it still matches c
// (guards against a new connection unregistering itself on the old conn's behalf).
func (h *hub) unregister(agentID string, c *agentConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[agentID] == c {
		delete(h.conns, agentID)
	}
}

// push serialises task as a WSMsgTask frame and enqueues it for delivery to
// agentID. Returns false if the agent has no live connection.
func (h *hub) push(agentID string, task *models.Task) bool {
	h.mu.RLock()
	c, ok := h.conns[agentID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	b, err := json.Marshal(models.WSMessage{Type: models.WSMsgTask, Task: task})
	if err != nil {
		utils.Logger.Errorf("hub: marshal task %s: %v", task.ID, err)
		return false
	}
	select {
	case c.send <- b:
		return true
	default:
		utils.Logger.Warnf("hub: send buffer full for agent %s — task %s stays pending", agentID, task.ID)
		return false
	}
}
