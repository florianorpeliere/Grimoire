// Package ollama is the embed.Embedder backed by a local Ollama server,
// called over its REST API (POST /api/embed).
//
// Over-long input: Ollama's embed API truncates an input longer than the
// model context unless "truncate" is false, and its default context for
// qwen3-embedding is 4,096 tokens. Every request sets "truncate": false, so
// Ollama answers HTTP 400 "the input length exceeds the context length"
// instead of truncating; Embed reports it as embed.ErrInputTooLong. Every
// request also sets the context to ContextTokens, above the longest SRD chunk.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/florianorpeliere/grimoire/internal/embed"
)

// EnvHost is the only place the server address is read from.
const EnvHost = "OLLAMA_HOST"

const (
	// Model is the embedding model, which must already be pulled on the server.
	Model = "qwen3-embedding:0.6b"
	// Dimension is the length of the vectors Model returns.
	Dimension = 1024
	// ContextTokens is the context requested for every call (num_ctx). The
	// model supports 32,768; 8,192 is about twice the longest SRD chunk.
	// Keep it the same for every caller: Ollama reloads the model when it
	// changes.
	ContextTokens = 8192
	// Retries is how many times a call is retried after a transient error
	// (network error, HTTP 429 or 5xx), so a call is tried at most
	// Retries+1 times. Other errors are not retried.
	Retries = 3
	// requestTimeout bounds one HTTP call, including the model load.
	requestTimeout = 2 * time.Minute
)

// inputTooLong is the error message Ollama returns, with HTTP 400, for an
// input longer than the context when truncate is false.
const inputTooLong = "the input length exceeds the context length"

var (
	// ErrNoHost is returned when OLLAMA_HOST is unset or blank.
	ErrNoHost = errors.New(EnvHost + " is not set")
	// ErrBadHost is returned when OLLAMA_HOST is not an http(s) URL with a host.
	ErrBadHost = errors.New(EnvHost + " must be an http:// or https:// URL with a host, e.g. http://localhost:11434")
)

// Client calls the Ollama server at a base URL.
type Client struct {
	base string
	http *http.Client
	// backoff is the wait before retry n (0-based).
	backoff func(n int) time.Duration
}

var _ embed.Embedder = (*Client)(nil)

// FromEnv returns a Client for the server at OLLAMA_HOST. There is
// deliberately no default address.
func FromEnv(getenv func(string) string) (*Client, error) {
	raw := strings.TrimSpace(getenv(EnvHost))
	if raw == "" {
		return nil, ErrNoHost
	}
	return New(raw)
}

// New returns a Client for the server at base, e.g. http://localhost:11434.
func New(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, ErrBadHost
	}
	return &Client{
		base:    strings.TrimRight(u.String(), "/"),
		http:    &http.Client{Timeout: requestTimeout},
		backoff: func(n int) time.Duration { return time.Second << n }, // 1 s, 2 s, 4 s
	}, nil
}

// Model returns Model.
func (c *Client) Model() string { return Model }

// Dimension returns Dimension.
func (c *Client) Dimension() int { return Dimension }

type embedRequest struct {
	Model    string         `json:"model"`
	Input    []string       `json:"input"`
	Truncate bool           `json:"truncate"`
	Options  map[string]any `json:"options"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Embed returns the vectors of texts, retrying transient errors up to
// Retries times. A text longer than ContextTokens fails the whole call with
// an error wrapping embed.ErrInputTooLong.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{
		Model:    Model,
		Input:    texts,
		Truncate: false,
		Options:  map[string]any{"num_ctx": ContextTokens},
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	for attempt := 0; ; attempt++ {
		vecs, transient, err := c.post(ctx, body)
		if err == nil {
			return c.check(vecs, len(texts))
		}
		if !transient || attempt == Retries || ctx.Err() != nil {
			if transient {
				return nil, fmt.Errorf("ollama: %w (tried %d times)", err, attempt+1)
			}
			return nil, fmt.Errorf("ollama: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ollama: %w", ctx.Err())
		case <-time.After(c.backoff(attempt)):
		}
	}
}

// post makes one call and says whether its error is worth retrying.
func (c *Client) post(ctx context.Context, body []byte) (vecs [][]float32, transient bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err // names the method and URL
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, true, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := serverMessage(data)
		transient := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(msg, inputTooLong) {
			return nil, false, fmt.Errorf("%w (%d tokens): %s", embed.ErrInputTooLong, ContextTokens, msg)
		}
		if resp.StatusCode == http.StatusNotFound && strings.Contains(msg, "not found") {
			msg += fmt.Sprintf(" (pull it on the Ollama server: ollama pull %s)", Model)
		}
		return nil, transient, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	var r embedResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false, fmt.Errorf("decode response: %w", err)
	}
	return r.Embeddings, false, nil
}

// check verifies the server returned one vector of Dimension per text.
func (c *Client) check(vecs [][]float32, n int) ([][]float32, error) {
	if len(vecs) != n {
		return nil, fmt.Errorf("ollama: got %d vectors for %d texts", len(vecs), n)
	}
	for i, v := range vecs {
		if len(v) != Dimension {
			return nil, fmt.Errorf("ollama: vector %d has %d dimensions, want %d for %s", i, len(v), Dimension, Model)
		}
	}
	return vecs, nil
}

// serverMessage returns Ollama's error message, or the raw body.
func serverMessage(data []byte) string {
	var e errorResponse
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(data))
}
