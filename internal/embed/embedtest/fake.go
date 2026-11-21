// Package embedtest provides a fake embed.Embedder for tests, so they run
// without an embedding server.
package embedtest

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"unicode/utf8"

	"github.com/florianorpeliere/grimoire/internal/embed"
)

// Fake is a deterministic embed.Embedder: the vector of a text depends only
// on the text and the dimension. It records the texts it was asked to embed.
type Fake struct {
	ModelName string
	Dim       int
	// MaxRunes, when positive, makes Embed fail with embed.ErrInputTooLong
	// for a batch holding a text longer than MaxRunes characters.
	MaxRunes int
	// Err, when set, is returned by every call to Embed.
	Err error

	mu    sync.Mutex
	calls [][]string
}

var _ embed.Embedder = (*Fake)(nil)

// Model returns ModelName.
func (f *Fake) Model() string { return f.ModelName }

// Dimension returns Dim.
func (f *Fake) Dimension() int { return f.Dim }

// Embed records texts and returns one vector per text.
func (f *Fake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		if f.MaxRunes > 0 && utf8.RuneCountInString(t) > f.MaxRunes {
			return nil, fmt.Errorf("fake: %w", embed.ErrInputTooLong)
		}
		vecs[i] = vector(t, f.Dim)
	}
	return vecs, nil
}

// Texts returns every text Embed was called with, in call order, including
// the texts of failed calls.
func (f *Fake) Texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []string
	for _, c := range f.calls {
		all = append(all, c...)
	}
	return all
}

// Reset forgets the recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// vector derives a vector of length dim from text.
func vector(text string, dim int) []float32 {
	h := fnv.New32a()
	h.Write([]byte(text))
	seed := h.Sum32()
	v := make([]float32, dim)
	for i := range v {
		seed = seed*1664525 + 1013904223
		v[i] = float32(seed%2001)/1000 - 1
	}
	return v
}
