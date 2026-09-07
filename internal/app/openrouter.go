package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const openRouterBase = "https://openrouter.ai/api/v1"

type ModelInfo struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ContextLength int     `json:"context_length"`
	PromptPrice   float64 `json:"prompt_price"`
	CompPrice     float64 `json:"completion_price"`
}

type KeyInfo struct {
	Label     string   `json:"label"`
	Usage     float64  `json:"usage"`
	Limit     *float64 `json:"limit"`
	Remaining *float64 `json:"remaining"`
}

type OpenRouter struct {
	key    string
	base   string // openRouterBase unless a test points it elsewhere
	client *http.Client
	// retryBase is the first wait between attempts; a test sets it to zero.
	retryBase time.Duration

	mu      sync.Mutex
	models  []ModelInfo
	fetched time.Time
	keyInfo *KeyInfo
	keyAt   time.Time
}

func NewOpenRouter(key string) *OpenRouter {
	return &OpenRouter{key: key, base: openRouterBase, retryBase: 2 * time.Second,
		client: &http.Client{Timeout: 10 * time.Minute}}
}

const (
	// A model call fails for two different reasons and only one is worth trying
	// again: the upstream was briefly unreachable, or the account cannot make the
	// call at all. Three attempts covers a blip and a rate limit without turning
	// an outage into a long wait, which the scheduler's own backoff handles.
	chatAttempts = 3
	// chatBudget bounds the whole call, retries included, so a job on a twenty
	// minute cadence cannot be held by one turn for an hour.
	chatBudget = 15 * time.Minute
)

// statusError is a non-200 the caller may want to classify. PermanentError is
// the subset that must never be retried.
type statusError struct {
	code       int
	msg        string
	retryAfter time.Duration
}

func (e *statusError) Error() string { return e.msg }

// retryableChat reports whether trying the same request again could succeed.
// Anything unrecognised is treated as worth one more attempt: a network that
// broke mid-handshake reports itself in too many ways to enumerate, and the
// attempt is cheap next to a reminder that never arrives.
func retryableChat(err error) bool {
	var perm *PermanentError
	if errors.As(err, &perm) {
		return false
	}
	var st *statusError
	if errors.As(err, &st) {
		return st.code == 408 || st.code == 409 || st.code == 425 ||
			st.code == 429 || st.code >= 500
	}
	return !errors.Is(err, context.Canceled)
}

// Chat calls the model, retrying a failure that could plausibly go away. It
// stops retrying the moment the answer starts arriving: the deltas are already
// on the operator's screen, and a second attempt would say the first half
// twice.
func (o *OpenRouter) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResult, error) {
	ctx, cancel := context.WithTimeout(ctx, chatBudget)
	defer cancel()

	var last error
	for attempt := 1; attempt <= chatAttempts; attempt++ {
		if attempt > 1 {
			if err := o.waitBeforeRetry(ctx, attempt, last); err != nil {
				return nil, last
			}
		}
		res, started, err := o.chatOnce(ctx, req, onDelta)
		if err == nil {
			return res, nil
		}
		last = err
		if started || ctx.Err() != nil || !retryableChat(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", chatAttempts, last)
}

// waitBeforeRetry sleeps between attempts, honouring a Retry-After the upstream
// asked for and otherwise doubling.
func (o *OpenRouter) waitBeforeRetry(ctx context.Context, attempt int, last error) error {
	wait := o.retryBase * time.Duration(1<<(attempt-2))
	var st *statusError
	if errors.As(last, &st) && st.retryAfter > 0 {
		wait = st.retryAfter
	}
	if wait <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (o *OpenRouter) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/local/agent")
	req.Header.Set("X-Title", "agent")
	return o.client.Do(req)
}

// Models returns tool-calling models, cached and refreshed daily.
func (o *OpenRouter) Models(ctx context.Context) ([]ModelInfo, error) {
	o.mu.Lock()
	if time.Since(o.fetched) < 24*time.Hour && len(o.models) > 0 {
		m := o.models
		o.mu.Unlock()
		return m, nil
	}
	o.mu.Unlock()

	resp, err := o.do(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			ContextLength       int      `json:"context_length"`
			SupportedParameters []string `json:"supported_parameters"`
			Pricing             struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, m := range payload.Data {
		if !contains(m.SupportedParameters, "tools") {
			continue
		}
		p, _ := strconv.ParseFloat(m.Pricing.Prompt, 64)
		c, _ := strconv.ParseFloat(m.Pricing.Completion, 64)
		out = append(out, ModelInfo{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength,
			PromptPrice: p, CompPrice: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	o.mu.Lock()
	o.models, o.fetched = out, time.Now()
	o.mu.Unlock()
	return out, nil
}

// filterModels narrows a catalogue to the models matching a free-text query.
// The query is split on whitespace and every term must appear, case-folded, in
// the model's id or display name, so "claude 4.5" finds claude-sonnet-4.5 and
// "anthropic/" finds a whole provider. An empty query keeps everything, and
// the catalogue's own order is preserved either way.
func filterModels(models []ModelInfo, q string) []ModelInfo {
	terms := strings.Fields(strings.ToLower(q))
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		hay := strings.ToLower(m.ID + " " + m.Name)
		if matchesAll(hay, terms) {
			out = append(out, m)
		}
	}
	return out
}

func matchesAll(hay string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func (o *OpenRouter) Key(ctx context.Context) (*KeyInfo, error) {
	o.mu.Lock()
	if time.Since(o.keyAt) < time.Hour && o.keyInfo != nil {
		k := o.keyInfo
		o.mu.Unlock()
		return k, nil
	}
	o.mu.Unlock()
	resp, err := o.do(ctx, "GET", "/key", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data struct {
			Label    string   `json:"label"`
			Usage    float64  `json:"usage"`
			Limit    *float64 `json:"limit"`
			LimitRem *float64 `json:"limit_remaining"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	k := &KeyInfo{Label: payload.Data.Label, Usage: payload.Data.Usage,
		Limit: payload.Data.Limit, Remaining: payload.Data.LimitRem}
	o.mu.Lock()
	o.keyInfo, o.keyAt = k, time.Now()
	o.mu.Unlock()
	return k, nil
}

type ToolSchema struct {
	Type     string       `json:"type"`
	Function ToolSchemaFn `json:"function"`
}

type ToolSchemaFn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []ToolSchema  `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	Usage    struct {
		Include bool `json:"include"`
	} `json:"usage"`
}

type ChatResult struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
	Err       error
}

// retryAfter reads the header of that name, which a rate limiter uses to say
// when it will answer again. Only the seconds form is honoured; a date form is
// ignored in favour of the ordinary backoff.
func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// PermanentError marks a failure that must not be retried.
type PermanentError struct{ msg string }

func (e *PermanentError) Error() string { return e.msg }

// Chat streams a completion. onDelta receives token text as it arrives.
// chatOnce is one attempt. It reports whether any of the answer reached the
// caller, because that is what decides whether the attempt can be repeated.
func (o *OpenRouter) chatOnce(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResult, bool, error) {
	req.Stream = true
	req.Usage.Include = true
	start := time.Now()
	started := false
	resp, err := o.do(ctx, "POST", "/chat/completions", req)
	if err != nil {
		return nil, started, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := fmt.Sprintf("openrouter %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		switch resp.StatusCode {
		case 401, 402, 403, 404:
			return nil, started, &PermanentError{msg}
		}
		return nil, started, &statusError{code: resp.StatusCode, msg: msg,
			retryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}

	res := &ChatResult{}
	calls := map[int]*ToolCall{}
	var order []int
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<16), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int     `json:"prompt_tokens"`
				CompletionTokens int     `json:"completion_tokens"`
				Cost             float64 `json:"cost"`
				PromptDetails    *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, started, fmt.Errorf("openrouter: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			res.Usage.PromptTokens = chunk.Usage.PromptTokens
			res.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			res.Usage.Cost = chunk.Usage.Cost
			if chunk.Usage.PromptDetails != nil {
				res.Usage.CachedTokens = chunk.Usage.PromptDetails.CachedTokens
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				// The answer has begun: from here the attempt cannot be repeated.
				started = true
				res.Text += ch.Delta.Content
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				started = true
				c, ok := calls[tc.Index]
				if !ok {
					c = &ToolCall{}
					calls[tc.Index] = c
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					c.ID = tc.ID
				}
				if tc.Function.Name != "" {
					c.Name += tc.Function.Name
				}
				c.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, started, err
	}
	sort.Ints(order)
	for _, i := range order {
		c := calls[i]
		if c.ID == "" {
			c.ID = fmt.Sprintf("call_%d_%d", start.UnixNano(), i)
		}
		if strings.TrimSpace(c.Arguments) == "" {
			c.Arguments = "{}"
		}
		res.ToolCalls = append(res.ToolCalls, *c)
	}
	if secs := time.Since(start).Seconds(); secs > 0 {
		res.Usage.TokensPerSecond = float64(res.Usage.CompletionTokens) / secs
	}
	return res, started, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
