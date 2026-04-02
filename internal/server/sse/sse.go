package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ddns-agent/internal/logger"
)

type Broker struct {
	mu          sync.RWMutex
	clients     map[chan string]struct{}
	maxClients  int
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

func (b *Broker) BroadcastNotification(level, message string) {
	data, _ := json.Marshal(map[string]string{"level": level, "message": message})
	b.broadcast("notification", string(data))
}

func (b *Broker) broadcast(event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			// slow client, skip
		}
	}
}

func (b *Broker) subscribe() (chan string, func()) {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
		close(ch)
	}
}

func (b *Broker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		b.mu.RLock()
		count := len(b.clients)
		b.mu.RUnlock()
		if count >= b.maxClients {
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		ch, unsub := b.subscribe()
		defer unsub()

		// send initial heartbeat
		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-ch:
				fmt.Fprint(w, msg)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	}
}
