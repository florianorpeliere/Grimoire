package dbtest

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// recorder stands in for *testing.T so Open's skips can be observed.
type recorder struct {
	testing.TB
	skipped string
	failed  string
}

func (r *recorder) Helper()        {}
func (r *recorder) Cleanup(func()) {}
func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestOpenSkips(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantSkip string
	}{
		{name: "unset", url: "", wantSkip: "DATABASE_URL is not set"},
		{name: "malformed", url: "postgres://%zz", wantSkip: "unreachable"},
		// Port 1 is reserved and nothing listens there: the connection is refused at once.
		{name: "unreachable", url: "postgres://127.0.0.1:1/grimoire_test?sslmode=disable&connect_timeout=2", wantSkip: "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.url)
			r := &recorder{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				Open(r)
			}()
			<-done
			if r.failed != "" {
				t.Fatalf("Open failed instead of skipping: %s", r.failed)
			}
			if !strings.Contains(r.skipped, tt.wantSkip) || !strings.Contains(r.skipped, "DATABASE_URL") {
				t.Errorf("skip message = %q, want it to name DATABASE_URL and contain %q", r.skipped, tt.wantSkip)
			}
		})
	}
}
