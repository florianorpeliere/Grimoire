package llmtest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/llm"
	"github.com/florianorpeliere/grimoire/internal/llm/llmtest"
)

func TestFake(t *testing.T) {
	cut := errors.New("stream cut")
	stop := errors.New("caller stops")
	out := int64(7)
	cost := 0.5
	tests := []struct {
		name       string
		fake       *llmtest.Fake
		model      string
		onTextErr  error
		wantDeltas []string
		wantErr    error
		wantName   string
		wantModel  string
		wantUsage  bool
	}{
		{
			name:       "full script with usage and cost",
			fake:       &llmtest.Fake{Deltas: []string{"a", "b"}, Result: llm.Result{StopReason: "end_turn", Usage: &llm.Usage{InputTokens: 3, OutputTokens: &out}, CostUSD: &cost}},
			model:      "m",
			wantDeltas: []string{"a", "b"},
			wantName:   llmtest.DefaultName,
			wantModel:  "m",
			wantUsage:  true,
		},
		{
			name:       "backend without usage, named",
			fake:       &llmtest.Fake{BackendName: "cli", Deltas: []string{"x"}, Result: llm.Result{Model: "reported"}},
			model:      "m",
			wantDeltas: []string{"x"},
			wantName:   "cli",
			wantModel:  "reported",
		},
		{
			name:       "cut stream",
			fake:       &llmtest.Fake{Deltas: []string{"a"}, Err: cut},
			model:      "m",
			wantDeltas: []string{"a"},
			wantErr:    cut,
			wantName:   llmtest.DefaultName,
			wantModel:  "m",
		},
		{
			name:       "callback error stops the stream",
			fake:       &llmtest.Fake{Deltas: []string{"a", "b"}},
			model:      "m",
			onTextErr:  stop,
			wantDeltas: []string{"a"},
			wantErr:    stop,
			wantName:   llmtest.DefaultName,
			wantModel:  "m",
		},
		{
			name:     "empty model",
			fake:     &llmtest.Fake{Deltas: []string{"a"}},
			model:    " ",
			wantErr:  llm.ErrNoModel,
			wantName: llmtest.DefaultName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			req := llm.Request{Model: tt.model, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}
			res, err := tt.fake.Generate(context.Background(), req, func(d string) error {
				got = append(got, d)
				return tt.onTextErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.wantDeltas) {
				t.Errorf("deltas = %q, want %q", got, tt.wantDeltas)
			}
			if res.Backend != tt.wantName || tt.fake.Name() != tt.wantName {
				t.Errorf("backend = %q, Name() = %q, want %q", res.Backend, tt.fake.Name(), tt.wantName)
			}
			if res.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", res.Model, tt.wantModel)
			}
			if (res.Usage != nil) != tt.wantUsage {
				t.Errorf("usage = %v, want present: %v", res.Usage, tt.wantUsage)
			}
			if reqs := tt.fake.Requests(); len(reqs) != 1 || reqs[0].Model != tt.model {
				t.Errorf("recorded %v, want the one request", reqs)
			}
		})
	}
}
