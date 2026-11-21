package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
)

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr error
	}{
		{name: "unset", host: "", wantErr: ollama.ErrNoHost},
		{name: "blank", host: "  ", wantErr: ollama.ErrNoHost},
		{name: "no scheme", host: "localhost:11434", wantErr: ollama.ErrBadHost},
		{name: "bind address", host: "0.0.0.0", wantErr: ollama.ErrBadHost},
		{name: "other scheme", host: "tcp://localhost:11434", wantErr: ollama.ErrBadHost},
		{name: "no host", host: "http://", wantErr: ollama.ErrBadHost},
		{name: "http URL", host: "http://localhost:11434"},
		{name: "https URL with trailing slash", host: "https://ollama.internal/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ollama.FromEnv(func(k string) string {
				if k == ollama.EnvHost {
					return tt.host
				}
				return ""
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (c.Model() != ollama.Model || c.Dimension() != ollama.Dimension) {
				t.Errorf("model %q dimension %d, want %q %d", c.Model(), c.Dimension(), ollama.Model, ollama.Dimension)
			}
		})
	}
}

// reply is one answer of the fake server.
type reply struct {
	status int
	body   string
}

func vectors(n, dim int) string {
	v := make([][]float32, n)
	for i := range v {
		v[i] = make([]float32, dim)
	}
	b, _ := json.Marshal(map[string]any{"model": ollama.Model, "embeddings": v})
	return string(b)
}

func TestEmbed(t *testing.T) {
	ok := reply{200, vectors(2, ollama.Dimension)}
	tests := []struct {
		name      string
		replies   []reply // the last one repeats
		wantCalls int
		wantErr   error
		// wantErrText is part of the error message; "" means no error.
		wantErrText string
	}{
		{name: "success", replies: []reply{ok}, wantCalls: 1},
		{name: "two 503 then success", replies: []reply{{503, `{"error":"busy"}`}, {503, `{"error":"busy"}`}, ok}, wantCalls: 3},
		{name: "429 then success", replies: []reply{{429, `{"error":"slow down"}`}, ok}, wantCalls: 2},
		{
			name: "persistent 500: retried, then the server message", replies: []reply{{500, `{"error":"model runner crashed"}`}},
			wantCalls: ollama.Retries + 1, wantErrText: "HTTP 500: model runner crashed (tried 4 times)",
		},
		{
			name: "input too long: not retried", replies: []reply{{400, `{"error":"the input length exceeds the context length"}`}},
			wantCalls: 1, wantErr: embed.ErrInputTooLong, wantErrText: "the input length exceeds the context length",
		},
		{
			name: "model missing: not retried, named", replies: []reply{{404, `{"error":"model 'qwen3-embedding:0.6b' not found"}`}},
			wantCalls: 1, wantErrText: "HTTP 404: model 'qwen3-embedding:0.6b' not found (pull it on the Ollama server",
		},
		{
			name: "other 400: not retried", replies: []reply{{400, `{"error":"invalid input type"}`}},
			wantCalls: 1, wantErrText: "HTTP 400: invalid input type",
		},
		{
			name: "plain-text error body", replies: []reply{{502, "bad gateway"}},
			wantCalls: ollama.Retries + 1, wantErrText: "HTTP 502: bad gateway",
		},
		{
			name: "wrong vector count", replies: []reply{{200, vectors(1, ollama.Dimension)}},
			wantCalls: 1, wantErrText: "got 1 vectors for 2 texts",
		},
		{
			name: "wrong dimension", replies: []reply{{200, vectors(2, 768)}},
			wantCalls: 1, wantErrText: "vector 0 has 768 dimensions, want 1024",
		},
		{
			name: "malformed JSON", replies: []reply{{200, "{"}},
			wantCalls: 1, wantErrText: "decode response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				checkRequest(t, r)
				rep := tt.replies[min(n, len(tt.replies))-1]
				w.WriteHeader(rep.status)
				io.WriteString(w, rep.body)
			}))
			defer srv.Close()
			c, err := ollama.New(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			ollama.NoBackoff(c)

			vecs, err := c.Embed(context.Background(), []string{"Fireball", "Shield"})
			if got := int(calls.Load()); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("Embed: %v", err)
				}
				if len(vecs) != 2 || len(vecs[0]) != ollama.Dimension {
					t.Errorf("got %d vectors", len(vecs))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErrText)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// checkRequest checks the request never lets Ollama truncate the input.
func checkRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
		t.Errorf("request %s %s, want POST /api/embed", r.Method, r.URL.Path)
	}
	var body struct {
		Model    string
		Input    []string
		Truncate *bool
		Options  map[string]float64
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode request: %v", err)
		return
	}
	if body.Model != ollama.Model {
		t.Errorf("model %q, want %q", body.Model, ollama.Model)
	}
	if body.Truncate == nil || *body.Truncate {
		t.Errorf("truncate = %v, want an explicit false", body.Truncate)
	}
	if body.Options["num_ctx"] != ollama.ContextTokens {
		t.Errorf("num_ctx = %v, want %d", body.Options["num_ctx"], ollama.ContextTokens)
	}
}

// An unreachable server is a transient error: retried, then reported.
func TestEmbedUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := ollama.New(url)
	if err != nil {
		t.Fatal(err)
	}
	ollama.NoBackoff(c)
	_, err = c.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "(tried 4 times)") {
		t.Fatalf("err = %v, want a connection error tried 4 times", err)
	}
}

// liveClient returns a client for the Ollama server at OLLAMA_HOST, or skips
// the test when it is not configured, unreachable, or lacks the model. It
// never pulls the model.
func liveClient(t *testing.T) *ollama.Client {
	t.Helper()
	c, err := ollama.FromEnv(os.Getenv)
	if errors.Is(err, ollama.ErrNoHost) {
		t.Skipf("skipping Ollama test: %s is not set", ollama.EnvHost)
	}
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(ollama.EnvHost)), "/")
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Get(base + "/api/tags")
	if err != nil {
		t.Skipf("skipping Ollama test: the server at %s is unreachable: %v", ollama.EnvHost, err)
	}
	defer resp.Body.Close()
	var tags struct{ Models []struct{ Name string } }
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Skipf("skipping Ollama test: cannot list the models at %s: %v", ollama.EnvHost, err)
	}
	for _, m := range tags.Models {
		if m.Name == ollama.Model {
			return c
		}
	}
	t.Skipf("skipping Ollama test: model %s is not pulled on the server (not pulled by tests)", ollama.Model)
	return nil
}

// sentence is about 12 tokens with the model's tokenizer.
const sentence = "The fighter swings a sword at the goblin and misses. "

func TestLiveEmbed(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tests := []struct {
		name    string
		text    string
		wantErr error
	}{
		{name: "short text", text: "Fireball"},
		// About 5,400 tokens: above Ollama's default context of 4,096, so
		// this passes only because ContextTokens is requested.
		{name: "above the default context", text: strings.Repeat(sentence, 450)},
		// About 12,000 tokens: above ContextTokens, refused, not truncated.
		{name: "above the requested context", text: strings.Repeat(sentence, 1000), wantErr: embed.ErrInputTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vecs, err := c.Embed(ctx, []string{tt.text})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (len(vecs) != 1 || len(vecs[0]) != ollama.Dimension) {
				t.Errorf("got %d vectors of %d dimensions", len(vecs), len(vecs[0]))
			}
		})
	}
}
