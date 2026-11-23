package claude_test

import (
	"math"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/llm"
	"github.com/florianorpeliere/grimoire/internal/llm/claude"
)

func TestCost(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		usage  llm.Usage
		want   float64
		wantOK bool
	}{
		{
			name:   "Opus 5.5, no cache",
			model:  "claude-opus-5-5",
			usage:  llm.Usage{InputTokens: 1_000_000, OutputTokens: i64(1_000_000)},
			want:   4 + 20,
			wantOK: true,
		},
		{
			name:  "Sonnet 5.5 with cache writes and reads",
			model: "claude-sonnet-5-5",
			usage: llm.Usage{InputTokens: 1_000_000, CacheCreationInputTokens: 3_000_000, CacheCreation5mInputTokens: 2_000_000,
				CacheCreation1hInputTokens: 1_000_000, CacheReadInputTokens: 10_000_000, OutputTokens: i64(1_000_000)},
			want:   2 + 2*2.50 + 4 + 10*0.10 + 10,
			wantOK: true,
		},
		{
			name:   "Fable 5.2",
			model:  "claude-fable-5-2",
			usage:  llm.Usage{InputTokens: 500_000, CacheReadInputTokens: 1_000_000, OutputTokens: i64(100_000)},
			want:   0.5*8 + 0.20 + 0.1*40,
			wantOK: true,
		},
		{
			name:   "Haiku 5.5 at 100,000 input tokens: standard price",
			model:  "claude-haiku-5-5",
			usage:  llm.Usage{InputTokens: 40_000, CacheReadInputTokens: 60_000, OutputTokens: i64(1_000_000)},
			want:   0.04*0.10 + 0.06*0.01 + 0.50,
			wantOK: true,
		},
		{
			name:   "Haiku 5.5 above 100,000 input tokens, cache reads included: long-context price",
			model:  "claude-haiku-5-5",
			usage:  llm.Usage{InputTokens: 40_000, CacheReadInputTokens: 60_001, OutputTokens: i64(1_000_000)},
			want:   0.04*0.50 + 0.060001*0.05 + 2.50,
			wantOK: true,
		},
		{
			name:   "zero tokens costs zero, known",
			model:  "claude-opus-5-5",
			usage:  llm.Usage{OutputTokens: i64(0)},
			want:   0,
			wantOK: true,
		},
		{
			name:  "unknown model",
			model: "claude-unpriced-test",
			usage: llm.Usage{InputTokens: 10, OutputTokens: i64(10)},
		},
		{
			name:  "output unknown",
			model: "claude-opus-5-5",
			usage: llm.Usage{InputTokens: 10},
		},
		{
			name:  "cache writes not split by duration",
			model: "claude-opus-5-5",
			usage: llm.Usage{InputTokens: 10, CacheCreationInputTokens: 100, CacheCreation5mInputTokens: 60, OutputTokens: i64(10)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := claude.Cost(tt.model, tt.usage)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok && got != 0 {
				t.Errorf("unknown cost returned %v, want 0 alongside ok=false", got)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cost = %v, want %v", got, tt.want)
			}
		})
	}
}
