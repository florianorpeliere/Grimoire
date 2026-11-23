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

func TestFakeCanceled(t *testing.T) {
	tests := []struct {
		name   string
		deltas []string
	}{
		{name: "with deltas", deltas: []string{"a", "b"}},
		{name: "without deltas", deltas: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			f := &llmtest.Fake{Deltas: tt.deltas}
			var got []string
			_, err := f.Generate(ctx, llm.Request{Model: "m"}, func(d string) error { got = append(got, d); return nil })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if len(got) != 0 {
				t.Errorf("deltas %q sent after cancel", got)
			}
		})
	}
}

func TestFakeResultIsACopy(t *testing.T) {
	out, cost := int64(7), 0.5
	f := &llmtest.Fake{Result: llm.Result{Usage: &llm.Usage{InputTokens: 3, OutputTokens: &out}, CostUSD: &cost}}
	tests := []struct {
		name   string
		mutate func(*llm.Result)
	}{
		{name: "input tokens", mutate: func(r *llm.Result) { r.Usage.InputTokens = 99 }},
		{name: "output tokens", mutate: func(r *llm.Result) { *r.Usage.OutputTokens = 99 }},
		{name: "cost", mutate: func(r *llm.Result) { *r.CostUSD = 99 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := f.Generate(context.Background(), llm.Request{Model: "m"}, func(string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&res)
			if f.Result.Usage.InputTokens != 3 || out != 7 || cost != 0.5 {
				t.Errorf("changing the result changed the script: %+v, output %d, cost %v", *f.Result.Usage, out, cost)
			}
		})
	}
}
