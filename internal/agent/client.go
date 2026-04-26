package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)

// Client communicates with the RasPiCD server over a persistent WebSocket.
type Client struct {
	serverURL string
	agentID   string
	secret    string
}

// NewClient creates a Client.
func NewClient(serverURL, agentID, secret string) *Client {
	return &Client{serverURL: serverURL, agentID: agentID, secret: secret}
}

// Connect establishes a WebSocket connection, registers this agent, and then
// drives the task receive/execute/report loop until the connection closes or
// ctx is cancelled.
//
// Returns nil only when ctx is cancelled (clean shutdown). Any other
// disconnection returns a non-nil error so the caller can retry.
func (c *Client) Connect(ctx context.Context, hostname, version string, exec *Executor) error {
	wsURL := httpToWS(c.serverURL) + "/api/v1/agents/ws"
	header := http.Header{"Authorization": []string{"Bearer " + c.secret}}

	wsc, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer wsc.Close()

	wsc.SetReadDeadline(time.Now().Add(models.WSPongWait))
	wsc.SetPongHandler(func(string) error {
		wsc.SetReadDeadline(time.Now().Add(models.WSPongWait))
		return nil
	})

	// send is the single write channel — gorilla requires one writer at a time.
	send := make(chan []byte, 32)

	// Write goroutine: drains send and emits pings.
	go func() {
		ticker := time.NewTicker(models.WSPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
				wsc.WriteMessage(websocket.CloseMessage, //nolint:errcheck
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				wsc.Close()
				return
			case msg, ok := <-send:
				if !ok {
					return
				}
				wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
				if err := wsc.WriteMessage(websocket.TextMessage, msg); err != nil {
					utils.Logger.Warnf("WS write error: %v", err)
					wsc.Close()
					return
				}
			case <-ticker.C:
				wsc.SetWriteDeadline(time.Now().Add(models.WSWriteWait))
				if err := wsc.WriteMessage(websocket.PingMessage, nil); err != nil {
					wsc.Close()
					return
				}
			}
		}
	}()

	// Send hello — the first frame the server expects.
	if err := sendMsg(send, models.WSMessage{
		Type:      models.WSMsgHello,
		AgentID:   c.agentID,
		Hostname:  hostname,
		Version:   version,
		IPAddress: localIP(),
	}); err != nil {
		return err
	}

	// Task queue: tasks are executed sequentially by a single goroutine so that
	// scripts on the Pi don't trample each other.
	taskCh := make(chan *models.Task, 8)
	defer close(taskCh)

	go func() {
		for task := range taskCh {
			sendMsg(send, models.WSMessage{ //nolint:errcheck
				Type:   models.WSMsgResult,
				TaskID: task.ID,
				Status: models.TaskStatusRunning,
			})

			result := exec.Run(task)

			sendMsg(send, models.WSMessage{ //nolint:errcheck
				Type:       models.WSMsgResult,
				TaskID:     task.ID,
				Status:     result.Status,
				Output:     result.Output,
				Error:      result.Error,
				DurationMs: result.DurationMs,
			})
		}
	}()

	// Read loop.
	for {
		_, raw, err := wsc.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown via ctx cancellation
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return fmt.Errorf("server closed connection")
			}
			return fmt.Errorf("connection lost: %w", err)
		}

		var msg models.WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			utils.Logger.Warnf("WS: unparseable message: %v", err)
			continue
		}

		if msg.Type == models.WSMsgTask && msg.Task != nil {
			utils.Logger.Infof("Received task %s (script: %s)", msg.Task.ID, msg.Task.Script)
			select {
			case taskCh <- msg.Task:
			case <-ctx.Done():
				return nil
			default:
				utils.Logger.Warnf("Task queue full — dropping task %s", msg.Task.ID)
			}
		}
	}
}

// sendMsg marshals msg and enqueues it on send. Returns an error only if
// marshalling fails (which should never happen for our fixed struct).
func sendMsg(send chan<- []byte, msg models.WSMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal ws message: %w", err)
	}
	send <- b
	return nil
}

// localIP returns the host's preferred outbound IP by dialling a UDP address.
// No packet is actually sent — the kernel just selects a route.
func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// httpToWS converts an http:// or https:// URL to its ws:// / wss:// equivalent.
func httpToWS(u string) string {
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}
