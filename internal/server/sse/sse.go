package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ddns-agent/internal/logger"
)

// Broker manages Server-Sent Event subscriptions and fan-out broadcasts.
type Broker struct {
	mu         sync.Mutex
	clients    map[chan string]struct{}
	maxClients int
}

func NewBroker(maxClients int) *Broker {
	return &Broker{
		clients:    make(map[chan string]struct{}),
		maxClients: maxClients,
	}
}

func (b *Broker) BroadcastLog(entry logger.LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	b.broadcast("log", string(data))
}

func (b *Broker) BroadcastRecords() { b.broadcast("records", "{}") }

func (b *Broker) broadcast(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			// Slow client — drop message rather than block the broadcaster.
		}
	}
}

// trySubscribe atomically checks the client limit and registers the channel.
// Returns (nil, nil, false) when the limit is reached.
func (b *Broker) trySubscribe() (chan string, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) >= b.maxClients {
		return nil, nil, false
	}
	ch := make(chan string, 64)
	b.clients[ch] = struct{}{}
	unsub := func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
		close(ch)
	}
	return ch, unsub, true
}

func (b *Broker) Handler(valid func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, unsub, ok := b.trySubscribe()
		if !ok {
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}
		defer unsub()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		control := http.NewResponseController(w)
		send := func(message string) bool {
			_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, message); err != nil {
				return false
			}
			if err := control.Flush(); err != nil {
				return false
			}
			_ = control.SetWriteDeadline(time.Time{})
			return true
		}
		if !send(": connected\n\n") {
			return
		}
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				if !valid() || !send(msg) {
					return
				}
			case <-ticker.C:
				if !valid() || !send(": heartbeat\n\n") {
					return
				}
			}
		}
	}
}
