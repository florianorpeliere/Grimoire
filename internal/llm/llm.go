// Package llm defines the answer generator that callers depend on. Backends
// live in their own packages (internal/llm/claude); callers only see the
// Generator interface, so a backend can be added without touching them.
package llm

import (
	"context"
	"errors"
	"strings"
	"time"
)

// EnvModel is the environment variable a caller may read the model name
// from, through ModelFromEnv. There is no default model: model choice is an
// open decision (CLAUDE.md).
const EnvModel = "GRIMOIRE_MODEL"

// ErrNoModel is returned when no model name is given.
var ErrNoModel = errors.New("no model name: pass one or set " + EnvModel)

// Role is the author of a message.
type Role string

// The roles a message can have.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of the conversation sent to the model.
type Message struct {
	Role Role
	Text string
}

// Request is one generation call.
type Request struct {
	// Model names the model. Required: an empty model is ErrNoModel.
	Model string
	// System is the system prompt. It may be empty.
	System string
	// Messages is the conversation, oldest first. At least one is required.
	Messages []Message
	// MaxTokens caps the output tokens. Zero lets the backend pick its own cap.
	MaxTokens int
}

// Usage counts the tokens of one call, as reported by the backend.
type Usage struct {
	// InputTokens is the input that was neither written to nor read from the
	// prompt cache. The full input is TotalInputTokens.
	InputTokens int64
	// CacheCreationInputTokens is the input written to the prompt cache, of
	// which CacheCreation5mInputTokens went to the 5-minute cache and
	// CacheCreation1hInputTokens to the 1-hour cache, when the backend
	// reports the split.
	CacheCreationInputTokens   int64
	CacheCreation5mInputTokens int64
	CacheCreation1hInputTokens int64
	// CacheReadInputTokens is the input read from the prompt cache.
	CacheReadInputTokens int64
	// OutputTokens is nil when the output count is unknown, for example when
	// the stream was cut before the backend sent its final count.
	OutputTokens *int64
}

// TotalInputTokens is the full input: uncached, cache writes and cache reads.
func (u Usage) TotalInputTokens() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// Result describes a finished, or interrupted, call.
type Result struct {
	// Backend names the implementation that produced the result.
	Backend string
	// Model is the model that answered, as reported by the backend, or the
	// requested model when the backend does not report it.
	Model string
	// StopReason is why generation stopped, as reported by the backend
	// (for the Claude API: end_turn, max_tokens, refusal...). Empty when
	// unknown.
	StopReason string
	// TimeToFirstText is the time from the request to the first text delta.
	// Zero when no text arrived.
	TimeToFirstText time.Duration
	// Usage is nil when the backend cannot report token usage.
	Usage *Usage
	// CostUSD is nil when the cost is unknown: the backend does not report
	// usage, the model has no known price, or a count is unknown. It is
	// never a placeholder zero.
	CostUSD *float64
}

// Generator sends a conversation to a model and streams the answer.
type Generator interface {
	// Name names the backend, as Result.Backend does.
	Name() string
	// Generate calls onText with each text delta as it arrives, in order,
	// then returns the result. If onText returns an error, generation stops
	// and that error is returned, wrapped. On any error the returned Result
	// still holds what is known so far (backend, model, usage), so that an
	// interrupted call can still be accounted for.
	Generate(ctx context.Context, req Request, onText func(delta string) error) (Result, error)
}

// ModelFromEnv returns model if it is not blank, else the value of EnvModel.
// It returns ErrNoModel when both are blank.
func ModelFromEnv(model string, getenv func(string) string) (string, error) {
	if m := strings.TrimSpace(model); m != "" {
		return m, nil
	}
	if m := strings.TrimSpace(getenv(EnvModel)); m != "" {
		return m, nil
	}
	return "", ErrNoModel
}
