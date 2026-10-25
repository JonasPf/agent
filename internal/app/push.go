package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Web push for unread messages (ADR-066). The agent cannot cause a push: one
// is sent only for an assistant message nobody has read, and its words are the
// transcript's. What lives here is the delivery — the keys, the devices, and
// the two RFCs a push service expects: the payload encrypted to the device
// (RFC 8291) and the request signed with the agent's key (RFC 8292).

// pushWait is how long a message stays unread before it is pushed. A page
// showing the conversation reads each message as it arrives, so the device
// being read on is quiet about the line on its screen.
const pushWait = 10 * time.Second

// pushTTL is how long a push service holds a push for a device that is off.
// The message is still unread tomorrow; a later one about the same session
// replaces it, by topic, rather than queueing behind it.
const pushTTL = 24 * time.Hour

// PushDevice is a registered device as the settings screen lists it. Its
// subscription keys are never served back.
type PushDevice struct {
	ID         string    `json:"id"`
	Service    string    `json:"service"`
	AddedAt    time.Time `json:"added_at"`
	LastSentAt time.Time `json:"last_sent_at,omitzero"`
	LastStatus int       `json:"last_status,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// pushTarget is a device with what pushing to it takes. Origin is the page
// that subscribed it, which the signed token names as its contact.
type pushTarget struct {
	PushDevice
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Origin   string `json:"origin"`
}

// pushFile is everything push keeps, in one file in the data directory. Not in
// the database: every tool is handed that, and a tool holding these could push
// to the operator's phone.
type pushFile struct {
	Private string       `json:"private"`
	Devices []pushTarget `json:"devices"`
}

// pushMessage is what a device is shown. The service worker displays it as it
// is; nothing about its wording is decided in the browser.
type pushMessage struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tag       string `json:"tag"`
	SessionID string `json:"session_id,omitempty"`
}

type Pusher struct {
	path   string
	key    *ecdsa.PrivateKey
	wait   time.Duration
	client *http.Client
	// unread is the message to push for a session, if one is still unread.
	unread func(sessionID string) (pushMessage, bool)

	mu      sync.Mutex
	devices []pushTarget
	// pending is the sessions with a push waiting. One wait per session: the
	// messages that arrive during it make one push, carrying the newest.
	pending map[string]bool
}

// newPusher loads the key and devices, making a key the first time. The key
// must survive a restart: every subscription is bound to it, and a new one
// would silently orphan every device.
func newPusher(dataDir string, unread func(string) (pushMessage, bool)) (*Pusher, error) {
	p := &Pusher{path: filepath.Join(dataDir, "push.json"), wait: pushWait, unread: unread,
		client: &http.Client{Timeout: 30 * time.Second}, pending: map[string]bool{}}
	var f pushFile
	b, err := os.ReadFile(p.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("%s: %w", p.path, err)
		}
		raw, err := b64url.DecodeString(f.Private)
		if err != nil {
			return nil, fmt.Errorf("%s: private key: %w", p.path, err)
		}
		if p.key, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), raw); err != nil {
			return nil, fmt.Errorf("%s: private key: %w", p.path, err)
		}
		p.devices = f.Devices
	case errors.Is(err, os.ErrNotExist):
		if p.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return nil, err
		}
		if err := p.save(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return p, nil
}

var b64url = base64.RawURLEncoding

// PublicKey is what a browser subscribes with: the uncompressed point, as
// PushManager.subscribe takes it.
func (p *Pusher) PublicKey() string {
	b, _ := p.key.PublicKey.Bytes()
	return b64url.EncodeToString(b)
}

// save writes the file whole and then moves it into place, so a crash leaves
// the old file or the new one and never half of either. The caller holds mu,
// or is the constructor.
func (p *Pusher) save() error {
	priv, err := p.key.Bytes()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(pushFile{Private: b64url.EncodeToString(priv), Devices: p.devices}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *Pusher) Devices() []PushDevice {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PushDevice, len(p.devices))
	for i, d := range p.devices {
		out[i] = d.PushDevice
	}
	return out
}

// pushSubscription is what PushSubscription.toJSON() returns in the browser.
type pushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// deviceID names a device by its endpoint, so registering one again finds it.
func deviceID(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:8])
}

// Register adds a device, or refreshes the keys of one already known. A
// subscription that could never be pushed to is refused here rather than
// failing quietly at the first message.
func (p *Pusher) Register(sub pushSubscription, origin string) (PushDevice, error) {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return PushDevice{}, fmt.Errorf("endpoint %q is not an https URL", sub.Endpoint)
	}
	if pub, err := b64url.DecodeString(sub.Keys.P256dh); err != nil {
		return PushDevice{}, fmt.Errorf("p256dh is not base64url: %v", err)
	} else if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return PushDevice{}, fmt.Errorf("p256dh is not a P-256 point: %v", err)
	}
	if auth, err := b64url.DecodeString(sub.Keys.Auth); err != nil || len(auth) != 16 {
		return PushDevice{}, errors.New("auth is not a 16-byte secret in base64url")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := deviceID(sub.Endpoint)
	t := pushTarget{PushDevice: PushDevice{ID: id, Service: u.Host, AddedAt: time.Now().UTC()},
		Endpoint: sub.Endpoint, P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth, Origin: origin}
	for i, d := range p.devices {
		if d.ID == id {
			t.PushDevice = d.PushDevice
			p.devices[i] = t
			return t.PushDevice, p.save()
		}
	}
	p.devices = append(p.devices, t)
	return t.PushDevice, p.save()
}

// Forget removes a device, and reports whether there was one.
func (p *Pusher) Forget(id string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, d := range p.devices {
		if d.ID == id {
			p.devices = append(p.devices[:i:i], p.devices[i+1:]...)
			return true, p.save()
		}
	}
	return false, nil
}

// Soon pushes a session's newest unread message once the wait is over, if it
// is still unread then. A wait already running for the session covers it.
func (p *Pusher) Soon(sessionID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.devices) == 0 || p.pending[sessionID] {
		return
	}
	p.pending[sessionID] = true
	time.AfterFunc(p.wait, func() {
		p.mu.Lock()
		delete(p.pending, sessionID)
		p.mu.Unlock()
		if m, ok := p.unread(sessionID); ok {
			p.Send(m)
		}
	})
}

// Send pushes a message to every device, and returns each with the answer. A
// device its push service calls gone is forgotten; any other refusal is kept
// beside it, where the settings screen shows it.
func (p *Pusher) Send(m pushMessage) []PushDevice {
	payload, _ := json.Marshal(m)
	p.mu.Lock()
	targets := append([]pushTarget(nil), p.devices...)
	p.mu.Unlock()

	type answer struct {
		status int
		err    string
	}
	answers := map[string]answer{}
	for _, t := range targets {
		status, err := p.deliver(t, payload, m.SessionID)
		a := answer{status: status}
		if err != nil {
			a.err = truncate(err.Error(), 300)
			log.Printf("push to %s: %s", t.Service, a.err)
		}
		answers[t.ID] = a
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	var kept []pushTarget
	var out []PushDevice
	for _, d := range p.devices {
		a, sent := answers[d.ID]
		if sent {
			d.LastSentAt, d.LastStatus, d.LastError = now, a.status, a.err
			out = append(out, d.PushDevice)
			if a.status == http.StatusNotFound || a.status == http.StatusGone {
				continue
			}
		}
		kept = append(kept, d)
	}
	p.devices = kept
	if err := p.save(); err != nil {
		log.Printf("push: %v", err)
	}
	return out
}

// deliver makes one push. Its status is what the push service answered, or
// zero when it could not be reached.
func (p *Pusher) deliver(t pushTarget, payload []byte, topic string) (int, error) {
	pub, err := b64url.DecodeString(t.P256dh)
	if err != nil {
		return 0, err
	}
	auth, err := b64url.DecodeString(t.Auth)
	if err != nil {
		return 0, err
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return 0, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return 0, err
	}
	body, err := encryptPush(payload, pub, auth, asPriv, salt)
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return 0, err
	}
	token, err := p.vapidToken(u.Scheme+"://"+u.Host, t.Origin)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(pushTTL.Seconds())))
	// High wakes a phone that is dozing; an unread message is what it is for.
	req.Header.Set("Urgency", "high")
	if topic != "" {
		// A session ID is twenty characters from the alphabet a topic allows,
		// so a push not yet delivered is replaced by the next about the same
		// conversation.
		req.Header.Set("Topic", topic)
	}
	req.Header.Set("Authorization", "vapid t="+token+", k="+p.PublicKey())
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	said, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	msg := strings.TrimSpace(string(said))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return resp.StatusCode, fmt.Errorf("%d %s", resp.StatusCode, msg)
}

// vapidToken is the JWT a push service checks (RFC 8292): for this service,
// for the next twelve hours, signed with the agent's key. The contact is the
// page that subscribed; Apple refuses a token without one.
func (p *Pusher) vapidToken(audience, origin string) (string, error) {
	if !strings.HasPrefix(origin, "https://") {
		origin = "mailto:agent@localhost"
	}
	head := b64url.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": audience, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": origin})
	signing := head + "." + b64url.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + b64url.EncodeToString(sig), nil
}

// encryptPush is RFC 8291: a key agreed between a fresh key of the agent's and
// the device's, mixed with the device's secret, seals the payload as a single
// aes128gcm record (RFC 8188) whose header carries the salt and the agent's
// half of the agreement.
func encryptPush(plain, uaPublic, auth []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, err
	}
	secret, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes()
	info := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	ikm, err := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	const recordSize = 4096
	head := make([]byte, 0, 21+len(asPublic))
	head = append(head, salt...)
	head = binary.BigEndian.AppendUint32(head, recordSize)
	head = append(head, byte(len(asPublic)))
	head = append(head, asPublic...)
	// 0x02 marks the last record; nothing follows it, so there is no padding.
	return gcm.Seal(head, nonce, append(append([]byte(nil), plain...), 2), nil), nil
}

// unreadMessage is the push for a session: its title, and the first line of
// the newest message after its read point. Nothing when there is none — it was
// read in the meantime, taken back, or the session is gone.
func (a *App) unreadMessage(sessionID string) (pushMessage, bool) {
	s := a.store.Session(sessionID)
	if s == nil {
		return pushMessage{}, false
	}
	live := liveEntries(a.store.Entries(sessionID))
	for i := len(live) - 1; i >= 0; i-- {
		e := live[i]
		if e.Seq <= s.ReadThrough {
			break
		}
		if !countsAsUnread(e) {
			continue
		}
		title := s.Title
		if title == "" {
			title = "agent"
		}
		line, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
		// Clipped by character, not byte: a lock screen shows a cut umlaut
		// as a replacement mark.
		body := []rune(strings.TrimSpace(line))
		if len(body) > 140 {
			body = append(body[:139], '…')
		}
		return pushMessage{Title: title, Body: string(body),
			Tag: "agent:" + sessionID, SessionID: sessionID}, true
	}
	return pushMessage{}, false
}

func (a *App) hPush(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"public_key": a.push.PublicKey(), "devices": a.push.Devices()})
}

func (a *App) hRegisterPush(w http.ResponseWriter, r *http.Request) {
	var sub pushSubscription
	if err := readJSON(r, &sub); err != nil {
		fail(w, 400, "bad body: %v", err)
		return
	}
	// The page that subscribed, as the browser names it. Behind the proxy the
	// host is the public one, since it passes the Host header through.
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "https://" + r.Host
	}
	d, err := a.push.Register(sub, origin)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) hForgetPush(w http.ResponseWriter, r *http.Request) {
	ok, err := a.push.Forget(r.PathValue("id"))
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	if !ok {
		fail(w, 404, "no such device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hTestPush pushes now, to every device, and answers with what each push
// service said. A push that fails looks exactly like a quiet agent; this is
// how the operator tells them apart.
func (a *App) hTestPush(w http.ResponseWriter, r *http.Request) {
	out := a.push.Send(pushMessage{Title: "agent", Tag: "agent:test",
		Body: "Test push. This device will be told about messages you have not read."})
	if out == nil {
		out = []PushDevice{}
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}
