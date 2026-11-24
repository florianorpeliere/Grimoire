package claude

import (
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// NewForTest returns a Client that calls the server at baseURL with key and
// retries mid-stream errors at once, so tests do not wait.
func NewForTest(key, baseURL string) *Client {
	c := newClient(key, option.WithBaseURL(baseURL))
	c.backoff = func(int) time.Duration { return 0 }
	return c
}
