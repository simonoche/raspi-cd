package server

import "sync"

// notifier wakes up handlers that are long-polling for a specific agent's tasks.
// Each agent may have at most one active waiter at a time.
type notifier struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}
}

func newNotifier() *notifier {
	return &notifier{waiters: make(map[string]chan struct{})}
}

// subscribe registers a waiter for agentID and returns a channel that is
// closed/sent on when a task is available. Call cancel when done waiting.
func (n *notifier) subscribe(agentID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.waiters[agentID] = ch
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		if n.waiters[agentID] == ch {
			delete(n.waiters, agentID)
		}
		n.mu.Unlock()
	}
}

// notify signals any handler currently waiting for agentID.
func (n *notifier) notify(agentID string) {
	n.mu.Lock()
	ch, ok := n.waiters[agentID]
	n.mu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default: // already notified, channel is buffered
		}
	}
}
