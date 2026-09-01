package app

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// wsEvent is everything that changes: transcript entries as they are appended,
// token deltas during generation, job ticks, dead letters, and breaker state.
type wsEvent struct {
	Kind         string        `json:"kind"`
	SessionID    string        `json:"session_id,omitempty"`
	Text         string        `json:"text,omitempty"`
	Entry        *Entry        `json:"entry,omitempty"`
	Notification *Notification `json:"notification,omitempty"`
}

type Hub struct {
	mu      sync.Mutex
	clients map[chan wsEvent]bool
}

func NewHub() *Hub { return &Hub{clients: map[chan wsEvent]bool{}} }

func (h *Hub) Broadcast(e wsEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- e:
		default: // a slow client misses messages and recovers by reading the HTTP endpoints
		}
	}
}

func (h *Hub) add() chan wsEvent {
	ch := make(chan wsEvent, 256)
	h.mu.Lock()
	h.clients[ch] = true
	h.mu.Unlock()
	return ch
}

func (h *Hub) remove(ch chan wsEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (a *App) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	ch := a.hub.add()
	defer a.hub.remove(ch)

	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if err := c.Ping(ctx); err != nil {
				return
			}
		case e := <-ch:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, c, e)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
