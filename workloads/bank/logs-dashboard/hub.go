package main

import "sync"

// hub fans a single source's log lines out to any number of connected SSE
// clients, and replays recent history to a client that connects mid-stream —
// without this, a browser tab opened partway through a demo would start on a
// blank panel until the next line happened to arrive.
type hub struct {
	mu      sync.Mutex
	history []string
	subs    map[chan string]struct{}
}

const hubHistorySize = 200

func newHub() *hub {
	return &hub{subs: make(map[chan string]struct{})}
}

func (h *hub) publish(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.history = append(h.history, line)
	if len(h.history) > hubHistorySize {
		h.history = h.history[len(h.history)-hubHistorySize:]
	}
	for ch := range h.subs {
		select {
		case ch <- line:
		default:
			// Slow subscriber: drop the line rather than block publishing to
			// everyone else. A missed line in a live demo log tail is far
			// less disruptive than the whole dashboard stalling.
		}
	}
}

// subscribe returns a channel of new lines plus a snapshot of recent history,
// and an unsubscribe func the caller must call when the client disconnects.
func (h *hub) subscribe() (ch chan string, history []string, unsubscribe func()) {
	ch = make(chan string, 64)
	h.mu.Lock()
	history = append([]string(nil), h.history...)
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe = func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
	return ch, history, unsubscribe
}
