// Package embed defines the embedding provider that ingestion and search
// depend on. Providers live in their own packages (internal/embed/ollama);
// callers only see the Embedder interface.
package embed

import (
	"context"
	"errors"
)

// ErrInputTooLong is returned, wrapped, by an Embedder when an input exceeds
// the model context. Providers must never truncate an input silently.
var ErrInputTooLong = errors.New("input exceeds the embedding model context")

// Embedder turns texts into vectors.
type Embedder interface {
	// Embed returns one vector per text, in the order of texts, each of
	// length Dimension. It fails, wrapping ErrInputTooLong, when a text is
	// longer than the model context, rather than truncate it.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model names the model that produces the vectors, as stored with them.
	Model() string
	// Dimension is the length of every vector.
	Dimension() int
}
