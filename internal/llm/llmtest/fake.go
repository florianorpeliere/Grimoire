// Package llmtest provides a fake llm.Generator for tests, so they run
// without calling a model.
package llmtest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/florianorpeliere/grimoire/internal/llm"
)

// DefaultName is the backend name of a Fake whose BackendName is empty.
const DefaultName = "fake"

// Fake is a scripted llm.Generator. Each call sends Deltas to onText, in
// order, then returns Result (with Backend and, if empty, Model filled in)
// and Err. Setting Err after some Deltas scripts a cut stream; leaving
// Result.Usage or Result.CostUSD nil scripts a backend that cannot report
// them. Fake records the requests it receives.
type Fake struct {
	BackendName string
	Deltas      []string
	Result      llm.Result
	Err         error

	mu       sync.Mutex
	requests []llm.Request
}

var _ llm.Generator = (*Fake)(nil)

// Name returns BackendName, or DefaultName when it is empty.
func (f *Fake) Name() string {
	if f.BackendName == "" {
		return DefaultName
	}
	return f.BackendName
}

// Generate records req and plays the script. Like a real backend, it fails
// with llm.ErrNoModel, before sending anything, when req.Model is blank.
func (f *Fake) Generate(_ context.Context, req llm.Request, onText func(string) error) (llm.Result, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	res := f.Result
	res.Backend = f.Name()
	if strings.TrimSpace(req.Model) == "" {
		return llm.Result{Backend: res.Backend}, llm.ErrNoModel
	}
	if res.Model == "" {
		res.Model = req.Model
	}
	for _, d := range f.Deltas {
		if err := onText(d); err != nil {
			return res, fmt.Errorf("text callback: %w", err)
		}
	}
	return res, f.Err
}

// Requests returns the requests Generate received, in call order.
func (f *Fake) Requests() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.requests...)
}
