package app

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Notification is either a silent badge update or an interrupting push.
type Notification struct {
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	SessionID string    `json:"session_id,omitempty"`
	JobID     string    `json:"job_id,omitempty"`
	Silent    bool      `json:"silent,omitempty"`
	Force     bool      `json:"force,omitempty"` // dead letters and the breaker cannot be muted
	At        time.Time `json:"at"`
}

// Notify delivers an interrupting notification. It carries its full text in the
// push payload, so displaying it needs no network fetch.
func (a *App) Notify(n Notification) {
	n.At = time.Now()
	a.hub.Broadcast(wsEvent{Kind: "notification", SessionID: n.SessionID, Notification: &n})
	if n.Silent {
		return
	}
	if !n.Force && n.SessionID != "" {
		if s := a.store.Session(n.SessionID); s != nil && s.Muted {
			return
		}
	}
	a.push(n)
}

func (a *App) push(n Notification) {
	pub, priv := a.vapidKeys()
	if pub == "" {
		return
	}
	subs, err := a.store.PushSubs()
	if err != nil {
		return
	}
	payload, _ := json.Marshal(n)
	for _, s := range subs {
		sub := &webpush.Subscription{Endpoint: s.Endpoint,
			Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}
		resp, err := webpush.SendNotification(payload, sub, &webpush.Options{
			Subscriber: a.cfg.VAPIDSubject, VAPIDPublicKey: pub, VAPIDPrivateKey: priv,
			TTL: 86400, Urgency: webpush.UrgencyHigh})
		if err != nil {
			log.Printf("push: %v", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			a.store.DeletePushSub(s.Endpoint)
		}
	}
}

type vapidFile struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// vapidKeys returns the configured keys, generating and storing a pair on first use.
func (a *App) vapidKeys() (string, string) {
	if a.cfg.VAPIDPublic != "" && a.cfg.VAPIDPrivate != "" {
		return a.cfg.VAPIDPublic, a.cfg.VAPIDPrivate
	}
	path := filepath.Join(a.cfg.DataDir, "vapid.json")
	if b, err := os.ReadFile(path); err == nil {
		var v vapidFile
		if json.Unmarshal(b, &v) == nil {
			a.cfg.VAPIDPublic, a.cfg.VAPIDPrivate = v.Public, v.Private
			return v.Public, v.Private
		}
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", ""
	}
	b, _ := json.Marshal(vapidFile{Public: pub, Private: priv})
	_ = os.WriteFile(path, b, 0o600)
	a.cfg.VAPIDPublic, a.cfg.VAPIDPrivate = pub, priv
	return pub, priv
}
