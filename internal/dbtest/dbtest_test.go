package dbtest

import (
	"context"
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
		{name: "malformed", url: "postgres://%zz", wantSkip: "explicit host, port and database name"},
		{name: "incomplete", url: "postgres://", wantSkip: "explicit host, port and database name"},
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

func TestCheckName(t *testing.T) {
	tests := []struct {
		name   string
		dbName string
		wantOK bool
	}{
		{name: "test database", dbName: "grimoire_test", wantOK: true},
		{name: "development database", dbName: "grimoire", wantOK: false},
		{name: "default database", dbName: "postgres", wantOK: false},
		{name: "prefix only", dbName: "grimoire_test_old", wantOK: false},
		{name: "empty", dbName: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkName(tt.dbName)
			if (err == nil) != tt.wantOK {
				t.Fatalf("checkName(%q) = %v, want ok=%v", tt.dbName, err, tt.wantOK)
			}
			if err != nil && !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Errorf("error %q does not name DATABASE_URL", err)
			}
		})
	}
}

// The guard inside the reset statement is exercised on grimoire_test by
// asking it to accept only another name: it must raise instead of dropping.
func TestResetStatementRefusesOtherDatabase(t *testing.T) {
	conn := Open(t)
	_, err := conn.ExecContext(context.Background(), resetStatement("not_the_current_database"))
	if err == nil || !strings.Contains(err.Error(), "refusing to reset database") {
		t.Fatalf("reset with a wrong expected name: err = %v, want a refusal", err)
	}
}
