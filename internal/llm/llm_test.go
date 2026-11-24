package llm_test

import (
	"errors"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/llm"
)

func TestModelFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		env     string
		want    string
		wantErr error
	}{
		{name: "explicit model", model: "model-a", env: "model-b", want: "model-a"},
		{name: "from the environment", model: "", env: "model-b", want: "model-b"},
		{name: "blank model falls back to the environment", model: "  ", env: " model-b ", want: "model-b"},
		{name: "neither", model: "", env: "", wantErr: llm.ErrNoModel},
		{name: "both blank", model: " ", env: "\t", wantErr: llm.ErrNoModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := llm.ModelFromEnv(tt.model, func(k string) string {
				if k == llm.EnvModel {
					return tt.env
				}
				return ""
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("model = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTotalInputTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage llm.Usage
		want  int64
	}{
		{name: "empty", usage: llm.Usage{}, want: 0},
		{name: "uncached only", usage: llm.Usage{InputTokens: 10}, want: 10},
		{name: "with cache writes and reads", usage: llm.Usage{InputTokens: 10, CacheCreationInputTokens: 200, CacheReadInputTokens: 3000}, want: 3210},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.TotalInputTokens(); got != tt.want {
				t.Errorf("TotalInputTokens = %d, want %d", got, tt.want)
			}
		})
	}
}
