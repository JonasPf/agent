package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	client *http.Client

	mu      sync.Mutex
	models  []ModelInfo
	fetched time.Time
	keyInfo *KeyInfo
	keyAt   time.Time
}

func NewOpenRouter(key string) *OpenRouter {
	return &OpenRouter{key: key, client: &http.Client{Timeout: 10 * time.Minute}}
}

func (o *OpenRouter) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, openRouterBase+path, r)
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

// PermanentError marks a failure that must not be retried.
type PermanentError struct{ msg string }

func (e *PermanentError) Error() string { return e.msg }

// Chat streams a completion. onDelta receives token text as it arrives.
func (o *OpenRouter) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResult, error) {
	req.Stream = true
	req.Usage.Include = true
	start := time.Now()
	resp, err := o.do(ctx, "POST", "/chat/completions", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := fmt.Sprintf("openrouter %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		switch resp.StatusCode {
		case 401, 402, 403, 404:
			return nil, &PermanentError{msg}
		}
		return nil, fmt.Errorf("%s", msg)
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
			return nil, fmt.Errorf("openrouter: %s", chunk.Error.Message)
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
				res.Text += ch.Delta.Content
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
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
		return nil, err
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
	return res, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
