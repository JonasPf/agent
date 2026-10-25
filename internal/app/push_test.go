package app

import (
	"bytes"
	"context"
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
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var b64 = base64.RawURLEncoding

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("base64 %q: %v", s, err)
	}
	return b
}

// The example in RFC 8291, Appendix A, with its fixed keys and salt. Matching it
// byte for byte is the only way to know the encryption is the one a browser
// undoes, short of a browser.
func TestPushEncryptionMatchesRFC8291(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(unb64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPublic := unb64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	auth := unb64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := unb64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	plain := unb64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")

	got, err := encryptPush(plain, uaPublic, auth, asPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	// The RFC gives the 86-byte header and the ciphertext separately.
	want := append(unb64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z 9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml"+
		"mlMoZIIgDll6e3vCYLocInmYWAmS6Tlz AC8wEqKK6PBru3jl7A8"),
		unb64(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrd irN6GcG7sFz1y1sqLgVi1VhjVkHsUoEs"+
			"bI_0LpXMuGvnzQ")...)
	if !bytes.Equal(got, want) {
		t.Errorf("body = %s\nwant   %s", b64.EncodeToString(got), b64.EncodeToString(want))
	}
}

// pushService stands in for a browser vendor's push service, for one
// subscribed device: it holds the device's keys, so it can undo what the agent
// sent the way the browser would.
type pushService struct {
	srv    *httptest.Server
	priv   *ecdh.PrivateKey
	auth   []byte
	got    chan delivery
	status int
	reply  string
}

type delivery struct {
	header http.Header
	body   []byte
}

func newPushService(t *testing.T, status int, reply string) *pushService {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	p := &pushService{priv: priv, auth: auth, got: make(chan delivery, 16), status: status, reply: reply}
	p.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.got <- delivery{header: r.Header.Clone(), body: body}
		w.WriteHeader(p.status)
		io.WriteString(w, p.reply)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pushService) endpoint() string { return p.srv.URL + "/push/abc123" }

// subscription is what the browser's PushSubscription.toJSON() hands the page.
func (p *pushService) subscription() string {
	b, _ := json.Marshal(map[string]any{
		"endpoint":       p.endpoint(),
		"expirationTime": nil,
		"keys": map[string]string{
			"p256dh": b64.EncodeToString(p.priv.PublicKey().Bytes()),
			"auth":   b64.EncodeToString(p.auth),
		},
	})
	return string(b)
}

func (p *pushService) next(t *testing.T, within time.Duration) delivery {
	t.Helper()
	select {
	case d := <-p.got:
		return d
	case <-time.After(within):
		t.Fatal("nothing was pushed")
		return delivery{}
	}
}

func (p *pushService) none(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case d := <-p.got:
		t.Fatalf("pushed when nothing should have been: %s", p.open(t, d.body))
	case <-time.After(within):
	}
}

// open undoes RFC 8291 as the receiving browser does, written out here rather
// than borrowed from the code under test.
func (p *pushService) open(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body of %d bytes has no header", len(body))
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs < 18 {
		t.Fatalf("record size %d", rs)
	}
	keyid, ct := body[21:21+idlen], body[21+idlen:]
	asPub, err := ecdh.P256().NewPublicKey(keyid)
	if err != nil {
		t.Fatalf("keyid is not a P-256 point: %v", err)
	}
	secret, err := p.priv.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), p.priv.PublicKey().Bytes()...), keyid...)
	ikm, _ := hkdf.Key(sha256.New, secret, p.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("the device could not decrypt the push: %v", err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatal("the last record does not end in the 0x02 delimiter")
	}
	return plain[:len(plain)-1]
}

type pushed struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tag       string `json:"tag"`
	SessionID string `json:"session_id"`
}

func (p *pushService) payload(t *testing.T, d delivery) pushed {
	t.Helper()
	var n pushed
	if err := json.Unmarshal(p.open(t, d.body), &n); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return n
}

// answeringModel gives each turn the next reply in order, and a request
// without tools — the title being asked for — "A title".
func answeringModel(t *testing.T, replies ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := "A title"
		if bytes.Contains(body, []byte(`"tools"`)) {
			mu.Lock()
			text = "done"
			if n < len(replies) {
				text = replies[n]
			}
			n++
			mu.Unlock()
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pushApp is an agent whose model gives these replies, with a wait short
// enough for a test and a client that trusts the fake push services.
func pushApp(t *testing.T, wait time.Duration, replies ...string) *App {
	t.Helper()
	a := newTestApp(t)
	srv := answeringModel(t, replies...)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	a.push.wait = wait
	a.push.client = testTLSClient()
	return a
}

// Every httptest TLS server presents the same certificate, so a client that
// trusts one trusts them all.
func testTLSClient() *http.Client {
	s := httptest.NewTLSServer(http.NotFoundHandler())
	defer s.Close()
	return s.Client()
}

type pushListing struct {
	PublicKey string       `json:"public_key"`
	Devices   []PushDevice `json:"devices"`
}

func pushState(t *testing.T, a *App) pushListing {
	t.Helper()
	w := call(t, a, "GET", "/push", "")
	if w.Code != 200 {
		t.Fatalf("GET /push: %d %s", w.Code, w.Body.String())
	}
	var l pushListing
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func register(t *testing.T, a *App, p *pushService) PushDevice {
	t.Helper()
	w := call(t, a, "POST", "/push/devices", p.subscription())
	if w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	var d PushDevice
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// verifyVAPID checks the Authorization header the way a push service does:
// the key it names is the agent's, the token is signed by it, and the token is
// for this push service.
func verifyVAPID(t *testing.T, a *App, d delivery, audience string) {
	t.Helper()
	h := d.header.Get("Authorization")
	if !strings.HasPrefix(h, "vapid ") {
		t.Fatalf("Authorization = %q, want the vapid scheme", h)
	}
	params := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(h, "vapid "), ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		params[k] = v
	}
	if params["k"] != pushState(t, a).PublicKey {
		t.Errorf("k = %q, want the public key the agent serves", params["k"])
	}
	parts := strings.Split(params["t"], ".")
	if len(parts) != 3 {
		t.Fatalf("t = %q is not a JWT", params["t"])
	}
	var head struct{ Alg, Typ string }
	_ = json.Unmarshal(unb64(t, parts[0]), &head)
	if head.Alg != "ES256" {
		t.Errorf("alg = %q, want ES256", head.Alg)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(unb64(t, parts[1]), &claims)
	if claims.Aud != audience {
		t.Errorf("aud = %q, want %q", claims.Aud, audience)
	}
	if exp := time.Unix(claims.Exp, 0); exp.Before(time.Now()) || exp.After(time.Now().Add(24*time.Hour)) {
		t.Errorf("exp = %v, want within the next day", exp)
	}
	if !strings.HasPrefix(claims.Sub, "https://") && !strings.HasPrefix(claims.Sub, "mailto:") {
		t.Errorf("sub = %q, want an https: or mailto: contact", claims.Sub)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), unb64(t, params["k"]))
	if err != nil {
		t.Fatal(err)
	}
	sig := unb64(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want 64 (r||s)", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Error("the token is not signed by the key the agent serves")
	}
}

func TestAnUnreadMessageIsPushedToARegisteredDevice(t *testing.T) {
	a := pushApp(t, 300*time.Millisecond, "The deploy finished.\nAll twelve services are up.")
	phone := newPushService(t, 201, "")
	register(t, a, phone)
	s := newSession(t, a)

	sendMessage(t, a, s.ID, "Deploy it.")

	d := phone.next(t, 5*time.Second)
	n := phone.payload(t, d)
	if n.Body != "The deploy finished." {
		t.Errorf("body = %q, want the newest message's first line", n.Body)
	}
	if n.Title != "A title" {
		t.Errorf("title = %q, want the conversation's title", n.Title)
	}
	if n.SessionID != s.ID || n.Tag != "agent:"+s.ID {
		t.Errorf("session_id = %q, tag = %q, want this session", n.SessionID, n.Tag)
	}
	if got := d.header.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Content-Encoding = %q", got)
	}
	if ttl, _ := strconv.Atoi(d.header.Get("TTL")); ttl <= 0 {
		t.Errorf("TTL = %q, want a positive number of seconds", d.header.Get("TTL"))
	}
	if got := d.header.Get("Urgency"); got != "high" {
		t.Errorf("Urgency = %q, want high, so a sleeping phone is woken", got)
	}
	if got := d.header.Get("Topic"); got != s.ID {
		t.Errorf("Topic = %q, want the session, so an undelivered push about it is replaced", got)
	}
	verifyVAPID(t, a, d, phone.srv.URL)
	if dev := pushState(t, a).Devices[0]; dev.LastStatus != 201 || dev.LastSentAt.IsZero() {
		t.Errorf("device after a push = %+v, want status 201 and a time", dev)
	}
}

// The device being read on must not buzz about the line on its screen. A read
// within the wait cancels the push; the second message, left unread, shows the
// wait was running all along.
func TestAMessageReadInTimeIsNotPushed(t *testing.T) {
	a := pushApp(t, 700*time.Millisecond, "First answer.", "Second answer.")
	phone := newPushService(t, 201, "")
	register(t, a, phone)
	s := newSession(t, a)

	sendMessage(t, a, s.ID, "One.")
	markRead(t, a, s.ID, "")
	phone.none(t, 1500*time.Millisecond)

	sendMessage(t, a, s.ID, "Two.")
	if n := phone.payload(t, phone.next(t, 5*time.Second)); n.Body != "Second answer." {
		t.Errorf("body = %q, want the unread second answer", n.Body)
	}
}

// A lock screen has room for a line, and a line cut through a character shows
// a replacement mark where the cut fell.
func TestALongMessageIsClippedByCharacter(t *testing.T) {
	a := pushApp(t, 100*time.Millisecond, strings.Repeat("ü", 300))
	phone := newPushService(t, 201, "")
	register(t, a, phone)
	s := newSession(t, a)

	sendMessage(t, a, s.ID, "Long.")

	n := phone.payload(t, phone.next(t, 5*time.Second))
	if want := strings.Repeat("ü", 139) + "…"; n.Body != want {
		t.Errorf("body = %q (%d runes), want 139 ü and an ellipsis", n.Body, len([]rune(n.Body)))
	}
}

// A turn writes a user message, an assistant line with a tool call, the tool's
// result, and the answer. Only what the agent said is pushed, and both of its
// lines inside the wait make one push carrying the newer.
func TestOnlyTheNewestAgentMessageIsPushed(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"content": "A title"}
		if bytes.Contains(body, []byte(`"tools"`)) {
			if bytes.Contains(body, []byte(`"role":"tool"`)) {
				delta = map[string]any{"content": "It is noon."}
			} else {
				delta = map[string]any{"content": "Looking now.", "tool_calls": []any{map[string]any{
					"index": 0, "id": "call_1", "function": map[string]any{"name": "no_such_tool", "arguments": "{}"}}}}
			}
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(model.Close)
	a := pushApp(t, 500*time.Millisecond)
	a.or.base = model.URL
	phone := newPushService(t, 201, "")
	register(t, a, phone)
	s := newSession(t, a)

	sendMessage(t, a, s.ID, "What time is it?")

	if n := phone.payload(t, phone.next(t, 5*time.Second)); n.Body != "It is noon." {
		t.Errorf("body = %q, want the newest agent message", n.Body)
	}
	phone.none(t, time.Second)
}

func TestAForkedCopyIsNotPushed(t *testing.T) {
	a := pushApp(t, 200*time.Millisecond, "Here it is.")
	phone := newPushService(t, 201, "")
	register(t, a, phone)
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "Show me.")
	phone.next(t, 5*time.Second)

	if w := call(t, a, "POST", "/sessions/"+s.ID+"/fork", "{}"); w.Code >= 300 {
		t.Fatalf("fork: %d %s", w.Code, w.Body.String())
	}
	phone.none(t, time.Second)
}

func TestATestPushReachesEveryDeviceAndReportsEachAnswer(t *testing.T) {
	a := pushApp(t, time.Hour)
	phone := newPushService(t, 201, "")
	laptop := newPushService(t, 403, "invalid JWT provided")
	register(t, a, phone)
	register(t, a, laptop)

	w := call(t, a, "POST", "/push/test", "")
	if w.Code != 200 {
		t.Fatalf("test push: %d %s", w.Code, w.Body.String())
	}
	var out struct{ Devices []PushDevice }
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	phone.payload(t, phone.next(t, time.Second))
	laptop.next(t, time.Second)
	if len(out.Devices) != 2 {
		t.Fatalf("devices = %+v, want both", out.Devices)
	}
	byStatus := map[int]PushDevice{}
	for _, d := range out.Devices {
		byStatus[d.LastStatus] = d
	}
	if _, ok := byStatus[201]; !ok {
		t.Errorf("devices = %+v, want one answered 201", out.Devices)
	}
	if bad, ok := byStatus[403]; !ok || !strings.Contains(bad.LastError, "invalid JWT") {
		t.Errorf("devices = %+v, want the refusal kept with what the service said", out.Devices)
	}
	// Kept, not forgotten: only a service saying the device is gone removes it.
	if got := len(pushState(t, a).Devices); got != 2 {
		t.Errorf("devices after a refusal = %d, want 2", got)
	}
}

func TestADeviceThePushServiceCallsGoneIsForgotten(t *testing.T) {
	for _, status := range []int{404, 410} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			a := pushApp(t, time.Hour)
			old := newPushService(t, status, "")
			register(t, a, old)
			call(t, a, "POST", "/push/test", "")
			old.next(t, time.Second)
			if got := pushState(t, a).Devices; len(got) != 0 {
				t.Errorf("devices = %+v, want the gone one forgotten", got)
			}
		})
	}
}

func TestRegisteringIsIdempotentAndForgettingIsFinal(t *testing.T) {
	a := pushApp(t, time.Hour)
	phone := newPushService(t, 201, "")
	first := register(t, a, phone)
	second := register(t, a, phone)
	if first.ID == "" || first.ID != second.ID {
		t.Errorf("ids = %q, %q, want one device named the same both times", first.ID, second.ID)
	}
	if got := pushState(t, a).Devices; len(got) != 1 || got[0].Service != strings.TrimPrefix(phone.srv.URL, "https://") {
		t.Fatalf("devices = %+v, want one, naming its push service", got)
	}
	if w := call(t, a, "DELETE", "/push/devices/"+first.ID, ""); w.Code != 204 {
		t.Errorf("forget: %d %s", w.Code, w.Body.String())
	}
	if got := pushState(t, a).Devices; len(got) != 0 {
		t.Errorf("devices after forgetting = %+v", got)
	}
	if w := call(t, a, "DELETE", "/push/devices/"+first.ID, ""); w.Code != 404 {
		t.Errorf("forgetting twice: %d, want 404", w.Code)
	}
	call(t, a, "POST", "/push/test", "")
	phone.none(t, 300*time.Millisecond)
}

func TestASubscriptionThatCannotBePushedToIsRefused(t *testing.T) {
	a := pushApp(t, time.Hour)
	phone := newPushService(t, 201, "")
	cases := map[string]func(m map[string]any){
		"plain http":  func(m map[string]any) { m["endpoint"] = "http://push.example/x" },
		"no endpoint": func(m map[string]any) { delete(m, "endpoint") },
		"not a point": func(m map[string]any) {
			m["keys"] = map[string]string{"p256dh": "AAAA", "auth": b64.EncodeToString(phone.auth)}
		},
		"short secret": func(m map[string]any) {
			m["keys"] = map[string]string{"p256dh": b64.EncodeToString(phone.priv.PublicKey().Bytes()), "auth": "AAAA"}
		},
		"not even JSON": nil,
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			body := "{"
			if spoil != nil {
				m := map[string]any{}
				_ = json.Unmarshal([]byte(phone.subscription()), &m)
				spoil(m)
				b, _ := json.Marshal(m)
				body = string(b)
			}
			if w := call(t, a, "POST", "/push/devices", body); w.Code != 400 {
				t.Errorf("status = %d, want 400 (%s)", w.Code, w.Body.String())
			}
		})
	}
	if got := pushState(t, a).Devices; len(got) != 0 {
		t.Errorf("devices = %+v, want none registered", got)
	}
}

// A restart must not change the key: every subscription is bound to it, and a
// new key would silently orphan every device.
func TestThePushKeySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := pushState(t, newTestAppAt(t, dir))
	if len(unb64(t, first.PublicKey)) != 65 {
		t.Fatalf("public key %q is not an uncompressed P-256 point", first.PublicKey)
	}
	if again := pushState(t, newTestAppAt(t, dir)); again.PublicKey != first.PublicKey {
		t.Errorf("key changed across a restart: %q → %q", first.PublicKey, again.PublicKey)
	}
	fi, err := os.Stat(filepath.Join(dir, "push.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("push.json mode = %v, want 0600", fi.Mode().Perm())
	}
}

// A tool holding the keys and a subscription could push to the operator's
// phone, which is the interrupt ADR-066 keeps from the agent. They are in the
// data directory, which a confined tool can neither read nor write.
func TestAToolCannotReachThePushKeys(t *testing.T) {
	volume := t.TempDir()
	data := filepath.Join(volume, "data")
	workspace := filepath.Join(volume, "workspace")
	st, err := OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: data, Workspace: workspace, ToolsDir: "../../tools"}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, DBPath(data), st.DB()),
		skills: NewSkills(cfg.SkillsDir, cfg.UserSkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}}
	if a.push, err = newPusher(data, a.unreadMessage); err != nil {
		t.Fatal(err)
	}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	register(t, a, newPushService(t, 201, ""))
	s := newSession(t, a)
	a.ensureWorkspace(s.ID)
	keys := filepath.Join(data, "push.json")
	before, err := os.ReadFile(keys)
	if err != nil {
		t.Fatal(err)
	}

	run := func(command string) toolResult {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"command": command})
		return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "bash", args)
	}
	read := run("cat " + keys)
	write := run("echo '{}' > " + keys)

	confinementOrSkip(t, a.sandbox)

	if read.OK && strings.Contains(read.Content, "private") {
		t.Errorf("a tool read the push keys: %q", read.Content)
	}
	if write.OK {
		t.Errorf("a tool overwrote the push keys: %q", write.Content)
	}
	if after, _ := os.ReadFile(keys); !bytes.Equal(before, after) {
		t.Error("the push keys changed under a tool")
	}
}
