package claude

import "github.com/florianorpeliere/grimoire/internal/llm"

// Price is what one model charges, in USD per million tokens.
type Price struct {
	Input        float64 // base input (not cached)
	CacheWrite5m float64 // writes to the 5-minute prompt cache
	CacheWrite1h float64 // writes to the 1-hour prompt cache
	CacheRead    float64 // cache hits and refreshes
	Output       float64
}

// modelPrice is the price of a model, with an optional higher price for
// requests whose full input (uncached, cache writes and cache reads) is above
// longAbove tokens.
type modelPrice struct {
	base      Price
	long      *Price
	longAbove int64
}

// prices holds the Claude API (first-party) standard prices of the current
// models.
//
// Source: https://platform.claude.com/docs/en/about-claude/pricing, section
// "Model pricing" and "Long context pricing", read on 2026-11-23 and every
// row re-checked against the page the same day. Update it by PR when the page
// changes. Not covered, because this client never uses them: the Batch API
// discount, fast mode, US-only inference (inference_geo, 1.1x), and server
// tools billed per use; a response reporting one of them gets an unknown cost
// (see standardPricing). A model missing here gets an unknown cost.
var prices = map[string]modelPrice{
	"claude-fable-5-2":  {base: Price{Input: 8, CacheWrite5m: 10, CacheWrite1h: 16, CacheRead: 0.20, Output: 40}},
	"claude-opus-5-5":   {base: Price{Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20}},
	"claude-sonnet-5-5": {base: Price{Input: 2, CacheWrite5m: 2.50, CacheWrite1h: 4, CacheRead: 0.10, Output: 10}},
	// Claude Haiku 5.5 is priced by prompt length: over 100,000 input tokens,
	// cache reads and writes included, the whole request pays the higher price.
	"claude-haiku-5-5": {
		base:      Price{Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50},
		long:      &Price{Input: 0.50, CacheWrite5m: 0.625, CacheWrite1h: 1, CacheRead: 0.05, Output: 2.50},
		longAbove: 100_000,
	},
}

// Cost returns the cost in USD of a call to model with usage u. ok is false,
// and the cost unknown, when the model has no known price, the output count
// is unknown, or the cache writes are not fully split between the 5-minute
// and 1-hour caches (they have different prices).
func Cost(model string, u llm.Usage) (usd float64, ok bool) {
	mp, found := prices[model]
	if !found || u.OutputTokens == nil {
		return 0, false
	}
	if u.CacheCreation5mInputTokens+u.CacheCreation1hInputTokens != u.CacheCreationInputTokens {
		return 0, false
	}
	p := mp.base
	if mp.long != nil && u.TotalInputTokens() > mp.longAbove {
		p = *mp.long
	}
	perMillion := float64(u.InputTokens)*p.Input +
		float64(u.CacheCreation5mInputTokens)*p.CacheWrite5m +
		float64(u.CacheCreation1hInputTokens)*p.CacheWrite1h +
		float64(u.CacheReadInputTokens)*p.CacheRead +
		float64(*u.OutputTokens)*p.Output
	return perMillion / 1e6, true
}
