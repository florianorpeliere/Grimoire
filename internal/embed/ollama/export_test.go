package ollama

import "time"

// NoBackoff makes c retry at once, so tests do not wait.
func NoBackoff(c *Client) { c.backoff = func(int) time.Duration { return 0 } }
