// Package claude is the llm.Generator backed by the Claude API (Messages API,
// streamed), through the official Go SDK.
//
// Usage in a streamed response (https://platform.claude.com/docs/en/build-with-claude/streaming
// and the MessageDeltaUsage fields of https://platform.claude.com/docs/en/api/messages/create):
// message_start carries the input counts (uncached, cache writes with their
// 5-minute/1-hour split, cache reads) and a placeholder output count, which
// is ignored. Each message_delta carries cumulative counts: its output count
// replaces the previous one, and its input counts, when present, replace the
// ones from message_start. The output count is known only once message_stop
// has arrived after a message_delta; a stream cut earlier reports it as
// unknown, and so its cost.
//
// The cost is reported as unknown (nil), never guessed, when:
//   - the model has no price in the table, or a count is unknown;
//   - the usage reports a service tier, speed or inference geography other
//     than the standard one, which the table does not price;
//   - an earlier attempt of the same call reached message_start and was then
//     retried: that attempt may have been billed, and the docs do not say.
//     Usage then describes the last attempt only.
package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/florianorpeliere/grimoire/internal/llm"
)

// EnvAPIKey is the only place the API key is read from.
const EnvAPIKey = "ANTHROPIC_API_KEY"

const (
	// Backend names this implementation in llm.Result.
	Backend = "claude-api"
	// DefaultMaxTokens caps the output when the request does not.
	DefaultMaxTokens = 4096
	// Retries bounds the retries of one Generate call: it sends at most
	// Retries+1 HTTP requests in total, whatever the mix of errors.
	//
	// Before the response starts, the SDK retries network errors, HTTP 408,
	// 409, 429 and 5xx (529 overloaded included) with exponential backoff,
	// honouring retry-after. Once the response has started (HTTP 200),
	// Generate retries an error event (overloaded_error, api_error,
	// rate_limit_error, timeout_error) or a cut connection, but only while no
	// text has been passed to the caller, so that no text is ever sent twice.
	// Other errors are returned at once.
	Retries = 2
)

var (
	// ErrNoAPIKey is returned when ANTHROPIC_API_KEY is unset or blank.
	ErrNoAPIKey = errors.New(EnvAPIKey + " is not set")
	// ErrIncompleteStream is returned when the response ends without its
	// final message_stop event.
	ErrIncompleteStream = errors.New("claude: response stream ended before message_stop")
	// ErrNoMessages is returned for a request without messages.
	ErrNoMessages = errors.New("claude: request has no messages")
)

// Client calls the Claude API.
type Client struct {
	api     anthropic.Client
	retries int
	// backoff is the wait before mid-stream retry n (0-based).
	backoff func(n int) time.Duration
}

var _ llm.Generator = (*Client)(nil)

// FromEnv returns a Client authenticated with the key in ANTHROPIC_API_KEY.
// No other credential source (auth token, profile, federation) is used.
func FromEnv(getenv func(string) string) (*Client, error) {
	key := strings.TrimSpace(getenv(EnvAPIKey))
	if key == "" {
		return nil, ErrNoAPIKey
	}
	return newClient(key), nil
}

func newClient(key string, opts ...option.RequestOption) *Client {
	base := []option.RequestOption{
		// Skip the SDK's own credential and environment lookup, so the key
		// comes from ANTHROPIC_API_KEY only.
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(key),
	}
	return &Client{
		api:     anthropic.NewClient(append(base, opts...)...),
		retries: Retries,
		backoff: backoff,
	}
}

// backoff doubles from 500ms, capped at 8s, like the SDK's own retries.
func backoff(n int) time.Duration {
	return min(500*time.Millisecond<<n, 8*time.Second)
}

// Name returns Backend.
func (c *Client) Name() string { return Backend }

// Generate streams the answer to req. See llm.Generator and the package
// documentation for retries and for when the cost is unknown.
func (c *Client) Generate(ctx context.Context, req llm.Request, onText func(string) error) (llm.Result, error) {
	res := llm.Result{Backend: Backend, Model: strings.TrimSpace(req.Model)}
	params, err := buildParams(req)
	if err != nil {
		return res, err
	}
	start := time.Now()
	budget := c.retries + 1 // HTTP requests left for this call
	discarded := false      // an earlier attempt reached message_start
	for attempt := 0; ; attempt++ {
		var a attemptState
		res, a, err = c.stream(ctx, start, params, onText, budget-1)
		budget -= a.requests
		if discarded {
			res.CostUSD = nil
		}
		if err == nil || a.emitted || !a.started || budget <= 0 || !transient(ctx, err) {
			return res, err
		}
		discarded = discarded || a.metered
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(c.backoff(attempt)):
		}
	}
}

// attemptState tells Generate what one attempt used and whether it can be
// retried.
type attemptState struct {
	requests int  // HTTP requests sent, the SDK's retries included
	started  bool // the response started (HTTP 200): the SDK's retries are over
	metered  bool // message_start reported usage: the attempt may be billed
	emitted  bool // text was passed to the caller
}

// errCallback marks an error returned by the caller's onText.
type errCallback struct{ err error }

func (e errCallback) Error() string { return "text callback: " + e.err.Error() }
func (e errCallback) Unwrap() error { return e.err }

// stream makes one streamed call, letting the SDK retry at most maxRetries
// times before the response starts.
func (c *Client) stream(ctx context.Context, start time.Time, params anthropic.MessageNewParams, onText func(string) error, maxRetries int) (llm.Result, attemptState, error) {
	res := llm.Result{Backend: Backend, Model: string(params.Model)}
	var (
		st       attemptState
		u        llm.Usage
		output   int64 // last cumulative output count
		hasDelta bool  // a message_delta reported an output count
		stopped  bool  // message_stop arrived
		unpriced bool  // the usage reports a billing modifier the table lacks
		err      error
	)
	count := func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		st.requests++
		return next(r)
	}
	s := c.api.Messages.NewStreaming(ctx, params, option.WithMaxRetries(maxRetries), option.WithMiddleware(count))
	defer s.Close()
	for err == nil && s.Next() {
		st.started = true
		switch ev := s.Current().AsAny().(type) {
		case anthropic.MessageStartEvent:
			if ev.Message.Model != "" {
				res.Model = string(ev.Message.Model)
			}
			mu := ev.Message.Usage
			st.metered = mu.JSON.InputTokens.Valid()
			u.InputTokens = mu.InputTokens
			u.CacheCreationInputTokens = mu.CacheCreationInputTokens
			u.CacheCreation5mInputTokens = mu.CacheCreation.Ephemeral5mInputTokens
			u.CacheCreation1hInputTokens = mu.CacheCreation.Ephemeral1hInputTokens
			u.CacheReadInputTokens = mu.CacheReadInputTokens
			// mu.OutputTokens is a placeholder: the count comes in message_delta.
			unpriced = !standardPricing(mu)
		case anthropic.ContentBlockDeltaEvent:
			td, ok := ev.Delta.AsAny().(anthropic.TextDelta)
			if !ok || td.Text == "" {
				continue
			}
			if !st.emitted {
				res.TimeToFirstText = time.Since(start)
				st.emitted = true
			}
			if cbErr := onText(td.Text); cbErr != nil {
				err = errCallback{cbErr}
			}
		case anthropic.MessageDeltaEvent:
			if ev.Delta.StopReason != "" {
				res.StopReason = string(ev.Delta.StopReason)
			}
			du := ev.Usage
			if du.JSON.InputTokens.Valid() {
				u.InputTokens = du.InputTokens
			}
			if du.JSON.CacheCreationInputTokens.Valid() {
				u.CacheCreationInputTokens = du.CacheCreationInputTokens
			}
			if du.JSON.CacheReadInputTokens.Valid() {
				u.CacheReadInputTokens = du.CacheReadInputTokens
			}
			if du.JSON.OutputTokens.Valid() {
				output, hasDelta = du.OutputTokens, true
			}
		case anthropic.MessageStopEvent:
			stopped = true
		}
	}
	if err == nil {
		err = s.Err()
		// An error event that comes first, before any decoded event, still
		// arrives after HTTP 200: the SDK builds it with the response status.
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusOK {
			st.started = true
		}
	}
	if err == nil && !stopped {
		err = ErrIncompleteStream
	}
	if st.metered {
		if stopped && hasDelta {
			u.OutputTokens = &output
		}
		res.Usage = &u
		if usd, ok := Cost(res.Model, u); ok && !unpriced {
			res.CostUSD = &usd
		}
	}
	return res, st, err
}

// standardPricing reports whether the usage was billed at the standard
// first-party prices of the table: standard service tier and speed, global
// inference geography (https://platform.claude.com/docs/en/about-claude/pricing).
// A field the response leaves out counts as standard.
func standardPricing(u anthropic.Usage) bool {
	if u.JSON.ServiceTier.Valid() && u.ServiceTier != anthropic.UsageServiceTierStandard {
		return false
	}
	if u.JSON.Speed.Valid() && u.Speed != anthropic.UsageSpeedStandard {
		return false
	}
	if u.JSON.InferenceGeo.Valid() && u.InferenceGeo != "" && u.InferenceGeo != "global" {
		return false
	}
	return true
}

// transient reports whether an error that happened after the response
// started is worth a retry.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var cb errCallback
	if errors.As(err, &cb) {
		return false
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch string(apiErr.Type()) {
		case "overloaded_error", "api_error", "rate_limit_error", "timeout_error":
			return true
		}
		return false
	}
	// A cut connection or a network error while reading the stream. Other
	// errors, such as an event that cannot be decoded, would fail again.
	var netErr net.Error
	return errors.Is(err, ErrIncompleteStream) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr)
}

// buildParams turns req into Messages API parameters.
func buildParams(req llm.Request) (anthropic.MessageNewParams, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return anthropic.MessageNewParams{}, llm.ErrNoModel
	}
	if len(req.Messages) == 0 {
		return anthropic.MessageNewParams{}, ErrNoMessages
	}
	if req.MaxTokens < 0 {
		return anthropic.MessageNewParams{}, fmt.Errorf("claude: MaxTokens is %d, want 0 or more", req.MaxTokens)
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for i, m := range req.Messages {
		block := anthropic.NewTextBlock(m.Text)
		switch m.Role {
		case llm.RoleUser:
			msgs = append(msgs, anthropic.NewUserMessage(block))
		case llm.RoleAssistant:
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		default:
			return anthropic.MessageNewParams{}, fmt.Errorf("claude: message %d has role %q, want %q or %q", i, m.Role, llm.RoleUser, llm.RoleAssistant)
		}
	}
	p := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	if req.System != "" {
		p.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	return p, nil
}
