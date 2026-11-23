package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/florianorpeliere/grimoire/internal/llm"
	"github.com/florianorpeliere/grimoire/internal/llm/claude"
)

// testKey is a fake key; tests check it never shows in errors.
const testKey = "test-key-not-a-real-secret-1234"

// SSE events, in the shape of the examples of
// https://platform.claude.com/docs/en/build-with-claude/streaming.
func ev(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }

func start(model, usage string) string {
	return ev("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"`+model+`","stop_reason":null,"stop_sequence":null,"usage":`+usage+`}}`)
}

const (
	blockStart = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	blockStop  = "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	ping       = "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	stop       = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	overloaded = "event: error\ndata: {\"type\": \"error\", \"error\": {\"type\": \"overloaded_error\", \"message\": \"Overloaded\"}}\n\n"
)

func text(s string) string {
	b, _ := json.Marshal(s)
	return ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(b)+`}}`)
}

func delta(stopReason, usage string) string {
	return ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stopReason+`","stop_sequence":null},"usage":`+usage+`}`)
}

// thinking is a thinking delta, which must not reach the caller.
var thinking = ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`)

// reply is one scripted HTTP response.
type reply struct {
	status int    // 0 means 200 with an event stream
	body   string // SSE events, or a JSON error body
}

// server plays one reply per request, in order, and records the requests.
type server struct {
	t       *testing.T
	replies []reply

	mu     sync.Mutex
	bodies []map[string]any
	keys   []string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n := len(s.bodies)
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	s.bodies = append(s.bodies, body)
	// The SDK sends the API key as a bearer token or in x-api-key.
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key == "" {
		key = r.Header.Get("x-api-key")
	}
	s.keys = append(s.keys, key)
	s.mu.Unlock()
	if r.URL.Path != "/v1/messages" {
		s.t.Errorf("path = %q, want /v1/messages", r.URL.Path)
	}
	if n >= len(s.replies) {
		s.t.Errorf("unexpected request %d", n+1)
		http.Error(w, "no more replies", http.StatusTeapot)
		return
	}
	rp := s.replies[n]
	if rp.status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("retry-after-ms", "0") // let the SDK retry at once
		w.WriteHeader(rp.status)
		_, _ = io.WriteString(w, rp.body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, rp.body)
}

func (s *server) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func i64(n int64) *int64 { return &n }

func question() llm.Request {
	return llm.Request{
		Model:    "claude-opus-5-5",
		System:   "Answer from the passages only.",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "What does the Grappled condition do?"}},
	}
}

func TestGenerate(t *testing.T) {
	const opus = "claude-opus-5-5"
	tests := []struct {
		name       string
		model      string
		replies    []reply
		wantText   string
		wantErr    error  // checked with errors.Is
		wantErrIn  string // substring of the error
		wantCalls  int
		wantStop   string
		wantUsage  *llm.Usage // nil: no usage
		wantCost   *float64   // nil: unknown
		wantModel  string
		wantNoTTFT bool
	}{
		{
			name:  "complete stream: placeholder output in message_start, final count in message_delta",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + ping + blockStart +
				text("Hello") + thinking + text(" world") + blockStop + delta("end_turn", `{"output_tokens":15}`) + stop}},
			wantText:  "Hello world",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25, OutputTokens: i64(15)},
			// 25 x $4 + 15 x $20 per million.
			wantCost:  ptr((25*4.0 + 15*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "cumulative message_delta: the last count wins, deltas are not added",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":10,"output_tokens":2}`) + blockStart + text("a") +
				delta("", `{"output_tokens":40}`) + text("b") + delta("end_turn", `{"output_tokens":90}`) + stop}},
			wantText:  "ab",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 10, OutputTokens: i64(90)},
			wantCost:  ptr((10*4.0 + 90*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "cached tokens: 5-minute and 1-hour writes and reads priced apart",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":100,"cache_creation_input_tokens":3000,"cache_read_input_tokens":20000,"cache_creation":{"ephemeral_5m_input_tokens":2000,"ephemeral_1h_input_tokens":1000},"output_tokens":1}`) +
				blockStart + text("ok") + delta("end_turn", `{"input_tokens":100,"cache_creation_input_tokens":3000,"cache_read_input_tokens":20000,"output_tokens":50}`) + stop}},
			wantText:  "ok",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 100, CacheCreationInputTokens: 3000, CacheCreation5mInputTokens: 2000, CacheCreation1hInputTokens: 1000, CacheReadInputTokens: 20000, OutputTokens: i64(50)},
			wantCost:  ptr((100*4.0 + 2000*5.0 + 1000*8.0 + 20000*0.20 + 50*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "input counts updated by message_delta replace message_start's",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":2679,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":3}`) +
				blockStart + text("x") + delta("end_turn", `{"input_tokens":10682,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":510}`) + stop}},
			wantText:  "x",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 10682, OutputTokens: i64(510)},
			wantCost:  ptr((10682*4.0 + 510*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "cache writes without a 5-minute/1-hour split: cost unknown",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":5,"cache_creation_input_tokens":300,"output_tokens":1}`) +
				blockStart + text("x") + delta("end_turn", `{"output_tokens":7}`) + stop}},
			wantText:  "x",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 5, CacheCreationInputTokens: 300, OutputTokens: i64(7)},
			wantModel: opus,
		},
		{
			name:  "model without a price: usage reported, cost unknown",
			model: "claude-unpriced-test",
			replies: []reply{{body: start("claude-unpriced-test", `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("x") +
				delta("end_turn", `{"output_tokens":15}`) + stop}},
			wantText:  "x",
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25, OutputTokens: i64(15)},
			wantModel: "claude-unpriced-test",
		},
		{
			name:      "stream cut after text, before message_delta: output and cost unknown, no retry",
			model:     opus,
			replies:   []reply{{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("Hel")}},
			wantText:  "Hel",
			wantErr:   claude.ErrIncompleteStream,
			wantCalls: 1,
			wantUsage: &llm.Usage{InputTokens: 25},
			wantModel: opus,
		},
		{
			name:  "stream cut after message_delta, before message_stop: output unknown",
			model: opus,
			replies: []reply{{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("Hi") + blockStop +
				delta("end_turn", `{"output_tokens":15}`)}},
			wantText:  "Hi",
			wantErr:   claude.ErrIncompleteStream,
			wantCalls: 1,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25},
			wantModel: opus,
		},
		{
			name:  "overloaded_error event before any text: retried",
			model: opus,
			replies: []reply{
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + ping + overloaded},
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("ok") + delta("end_turn", `{"output_tokens":4}`) + stop},
			},
			wantText:  "ok",
			wantCalls: 2,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25, OutputTokens: i64(4)},
			wantCost:  ptr((25*4.0 + 4*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "overloaded_error event after text: not retried, output unknown",
			model: opus,
			replies: []reply{
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("par") + overloaded},
			},
			wantText:  "par",
			wantErrIn: "Overloaded",
			wantCalls: 1,
			wantUsage: &llm.Usage{InputTokens: 25},
			wantModel: opus,
		},
		{
			name:  "cut before any text: retried, at most Retries times",
			model: opus,
			replies: []reply{
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`)},
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`)},
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`)},
			},
			wantErr:    claude.ErrIncompleteStream,
			wantCalls:  claude.Retries + 1,
			wantUsage:  &llm.Usage{InputTokens: 25},
			wantModel:  opus,
			wantNoTTFT: true,
		},
		{
			name:  "HTTP 529 overloaded then success: retried by the SDK",
			model: opus,
			replies: []reply{
				{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("ok") + delta("end_turn", `{"output_tokens":4}`) + stop},
			},
			wantText:  "ok",
			wantCalls: 2,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25, OutputTokens: i64(4)},
			wantCost:  ptr((25*4.0 + 4*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "HTTP 429 then 500 then success",
			model: opus,
			replies: []reply{
				{status: 429, body: `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited"}}`},
				{status: 500, body: `{"type":"error","error":{"type":"api_error","message":"Internal"}}`},
				{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart + text("ok") + delta("end_turn", `{"output_tokens":4}`) + stop},
			},
			wantText:  "ok",
			wantCalls: 3,
			wantStop:  "end_turn",
			wantUsage: &llm.Usage{InputTokens: 25, OutputTokens: i64(4)},
			wantCost:  ptr((25*4.0 + 4*20.0) / 1e6),
			wantModel: opus,
		},
		{
			name:  "HTTP 529 every time: bounded retries, API message returned",
			model: opus,
			replies: []reply{
				{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
				{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
				{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
			},
			wantErrIn:  "Overloaded",
			wantCalls:  claude.Retries + 1,
			wantModel:  opus,
			wantNoTTFT: true,
		},
		{
			name:  "HTTP 400: not retried, API message returned",
			model: opus,
			replies: []reply{
				{status: 400, body: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too large"}}`},
			},
			wantErrIn:  "max_tokens: too large",
			wantCalls:  1,
			wantModel:  opus,
			wantNoTTFT: true,
		},
		{
			name:  "HTTP 401: not retried",
			model: opus,
			replies: []reply{
				{status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`},
			},
			wantErrIn:  "invalid x-api-key",
			wantCalls:  1,
			wantModel:  opus,
			wantNoTTFT: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{t: t, replies: tt.replies}
			ts := httptest.NewServer(srv)
			defer ts.Close()
			c := claude.NewForTest(testKey, ts.URL)

			req := question()
			req.Model = tt.model
			var got strings.Builder
			res, err := c.Generate(context.Background(), req, func(d string) error {
				got.WriteString(d)
				return nil
			})

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.wantErrIn != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErrIn) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErrIn)
				}
			case err != nil:
				t.Fatalf("err = %v", err)
			}
			if err != nil && strings.Contains(err.Error(), testKey) {
				t.Errorf("the API key appears in the error: %v", err)
			}
			if got.String() != tt.wantText {
				t.Errorf("text = %q, want %q", got.String(), tt.wantText)
			}
			if n := srv.calls(); n != tt.wantCalls {
				t.Errorf("%d HTTP calls, want %d", n, tt.wantCalls)
			}
			if res.Backend != claude.Backend {
				t.Errorf("backend = %q, want %q", res.Backend, claude.Backend)
			}
			if res.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", res.Model, tt.wantModel)
			}
			if res.StopReason != tt.wantStop {
				t.Errorf("stop reason = %q, want %q", res.StopReason, tt.wantStop)
			}
			if tt.wantText != "" && res.TimeToFirstText <= 0 {
				t.Errorf("time to first text = %v, want > 0", res.TimeToFirstText)
			}
			if tt.wantNoTTFT && res.TimeToFirstText != 0 {
				t.Errorf("time to first text = %v, want 0", res.TimeToFirstText)
			}
			checkUsage(t, res.Usage, tt.wantUsage)
			checkCost(t, res.CostUSD, tt.wantCost)
		})
	}
}

func ptr(f float64) *float64 { return &f }

func checkUsage(t *testing.T, got, want *llm.Usage) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if got == nil {
		return
	}
	g, w := *got, *want
	if (g.OutputTokens == nil) != (w.OutputTokens == nil) || (g.OutputTokens != nil && *g.OutputTokens != *w.OutputTokens) {
		t.Errorf("output tokens = %v, want %v", fmtCount(g.OutputTokens), fmtCount(w.OutputTokens))
	}
	g.OutputTokens, w.OutputTokens = nil, nil
	if g != w {
		t.Errorf("usage = %+v, want %+v", g, w)
	}
}

func fmtCount(n *int64) string {
	if n == nil {
		return "unknown"
	}
	return fmt.Sprint(*n)
}

func checkCost(t *testing.T, got, want *float64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("cost = %v, want %v", fmtCost(got), fmtCost(want))
	case math.Abs(*got-*want) > 1e-12:
		t.Errorf("cost = %v, want %v", *got, *want)
	}
}

func fmtCost(f *float64) string {
	if f == nil {
		return "unknown"
	}
	return fmt.Sprint(*f)
}

func TestGenerateRequest(t *testing.T) {
	const opus = "claude-opus-5-5"
	ok := []reply{{body: start(opus, `{"input_tokens":1,"output_tokens":1}`) + delta("end_turn", `{"output_tokens":1}`) + stop}}
	tests := []struct {
		name          string
		req           llm.Request
		wantMaxTokens float64
		wantSystem    bool
		wantMessages  int
	}{
		{name: "default max tokens and a system prompt", req: question(), wantMaxTokens: claude.DefaultMaxTokens, wantSystem: true, wantMessages: 1},
		{
			name: "explicit max tokens, no system prompt, several turns",
			req: llm.Request{Model: opus, MaxTokens: 300, Messages: []llm.Message{
				{Role: llm.RoleUser, Text: "q1"}, {Role: llm.RoleAssistant, Text: "a1"}, {Role: llm.RoleUser, Text: "q2"},
			}},
			wantMaxTokens: 300,
			wantMessages:  3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{t: t, replies: ok}
			ts := httptest.NewServer(srv)
			defer ts.Close()
			if _, err := claude.NewForTest(testKey, ts.URL).Generate(context.Background(), tt.req, func(string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			body, key := srv.bodies[0], srv.keys[0]
			if key != testKey {
				t.Errorf("the request does not carry the configured key")
			}
			if body["model"] != tt.req.Model || body["stream"] != true || body["max_tokens"] != tt.wantMaxTokens {
				t.Errorf("model, stream, max_tokens = %v, %v, %v", body["model"], body["stream"], body["max_tokens"])
			}
			if _, has := body["system"]; has != tt.wantSystem {
				t.Errorf("system sent: %v, want %v", has, tt.wantSystem)
			}
			if msgs, _ := body["messages"].([]any); len(msgs) != tt.wantMessages {
				t.Errorf("%d messages sent, want %d", len(msgs), tt.wantMessages)
			}
			if _, has := body["thinking"]; has {
				t.Errorf("thinking sent; the model's default must apply")
			}
		})
	}
}

func TestGenerateInvalidRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     llm.Request
		wantErr error
	}{
		{name: "empty model", req: llm.Request{Model: " ", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}, wantErr: llm.ErrNoModel},
		{name: "no messages", req: llm.Request{Model: "claude-opus-5-5"}, wantErr: claude.ErrNoMessages},
		{name: "unknown role", req: llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{{Role: "system", Text: "q"}}}},
		{name: "negative max tokens", req: llm.Request{Model: "claude-opus-5-5", MaxTokens: -1, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{t: t}
			ts := httptest.NewServer(srv)
			defer ts.Close()
			res, err := claude.NewForTest(testKey, ts.URL).Generate(context.Background(), tt.req, func(string) error { return nil })
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if srv.calls() != 0 {
				t.Errorf("%d HTTP calls, want none", srv.calls())
			}
			if res.Backend != claude.Backend || res.Usage != nil || res.CostUSD != nil {
				t.Errorf("result = %+v, want the backend name only", res)
			}
		})
	}
}

func TestGenerateCallbackError(t *testing.T) {
	const opus = "claude-opus-5-5"
	srv := &server{t: t, replies: []reply{{body: start(opus, `{"input_tokens":25,"output_tokens":1}`) + blockStart +
		text("a") + text("b") + delta("end_turn", `{"output_tokens":15}`) + stop}}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	stopErr := errors.New("client went away")
	var got []string
	res, err := claude.NewForTest(testKey, ts.URL).Generate(context.Background(), question(), func(d string) error {
		got = append(got, d)
		return stopErr
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("err = %v, want %v", err, stopErr)
	}
	if len(got) != 1 || srv.calls() != 1 {
		t.Errorf("deltas %q after %d calls, want one delta and one call", got, srv.calls())
	}
	if res.Usage == nil || res.Usage.OutputTokens != nil || res.CostUSD != nil {
		t.Errorf("usage %+v, cost %v: want input known, output and cost unknown", res.Usage, fmtCost(res.CostUSD))
	}
}

func TestGenerateCanceled(t *testing.T) {
	srv := &server{t: t}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := claude.NewForTest(testKey, ts.URL).Generate(ctx, question(), func(string) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr error
	}{
		{name: "unset", key: "", wantErr: claude.ErrNoAPIKey},
		{name: "blank", key: "  ", wantErr: claude.ErrNoAPIKey},
		{name: "set", key: testKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := claude.FromEnv(func(k string) string {
				if k == claude.EnvAPIKey {
					return tt.key
				}
				return ""
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), claude.EnvAPIKey) {
					t.Errorf("error %q does not name %s", err, claude.EnvAPIKey)
				}
				return
			}
			if c.Name() != claude.Backend {
				t.Errorf("Name() = %q, want %q", c.Name(), claude.Backend)
			}
		})
	}
}

// EnvLiveTest turns on TestLiveAPI. It calls the real Claude API, which
// costs money.
const EnvLiveTest = "GRIMOIRE_CLAUDE_LIVE_TEST"

// TestLiveAPI makes one real, paid call. It runs only when
// GRIMOIRE_CLAUDE_LIVE_TEST=1, with ANTHROPIC_API_KEY and GRIMOIRE_MODEL set.
func TestLiveAPI(t *testing.T) {
	if os.Getenv(EnvLiveTest) != "1" {
		t.Skip("skipped: calls the real Claude API, which costs money; set " + EnvLiveTest + "=1, " + claude.EnvAPIKey + " and " + llm.EnvModel + " to run it")
	}
	model, err := llm.ModelFromEnv("", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	c, err := claude.FromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	res, err := c.Generate(context.Background(), llm.Request{
		Model:     model,
		MaxTokens: 64,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: "Reply with the single word: ready"}},
	}, func(d string) error { got.WriteString(d); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() == 0 || res.Usage == nil || res.Usage.OutputTokens == nil {
		t.Fatalf("text %q, usage %+v: want text and a full usage", got.String(), res.Usage)
	}
	t.Logf("model %s, stop %s, first text after %v, input %d, output %d, cost %s",
		res.Model, res.StopReason, res.TimeToFirstText, res.Usage.TotalInputTokens(), *res.Usage.OutputTokens, fmtCost(res.CostUSD))
}

// TestAPIErrorIsSDKError checks that API errors reach callers as the SDK's
// error type, so they can inspect the status and error type.
func TestAPIErrorIsSDKError(t *testing.T) {
	srv := &server{t: t, replies: []reply{{status: 400, body: `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`}}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	_, err := claude.NewForTest(testKey, ts.URL).Generate(context.Background(), question(), func(string) error { return nil })
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want an *anthropic.Error with status 400", err)
	}
}
