package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// recorder stands in for *testing.T so Open's skips and failures can be observed.
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

// runOpen calls Open on a recorder in its own goroutine, since Skipf and
// Fatalf end the calling goroutine.
func runOpen() *recorder {
	r := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Open(r)
	}()
	<-done
	return r
}

// None of these cases reach a running PostgreSQL server.
func TestOpen(t *testing.T) {
	tests := []struct {
		name string
		// testURL is TEST_DATABASE_URL; nil leaves it unset.
		testURL *string
		// appURL is DATABASE_URL, which Open must ignore.
		appURL   string
		wantSkip string
		wantFail string
	}{
		{name: "unset", wantSkip: "TEST_DATABASE_URL is not set (point it at the grimoire_test database"},
		{
			name:     "unset, DATABASE_URL on the development database",
			appURL:   "postgres://dev@127.0.0.1:1/grimoire?sslmode=disable",
			wantSkip: "TEST_DATABASE_URL is not set (point it at the grimoire_test database",
		},
		{name: "empty", testURL: ptr("  "), wantFail: "TEST_DATABASE_URL is set but empty"},
		{name: "malformed", testURL: ptr("postgres://%zz"), wantFail: "TEST_DATABASE_URL is incomplete or malformed"},
		{name: "incomplete", testURL: ptr("postgres://"), wantFail: "TEST_DATABASE_URL is incomplete or malformed"},
		{name: "missing port", testURL: ptr("postgres://127.0.0.1/grimoire_test"), wantFail: "TEST_DATABASE_URL is incomplete or malformed"},
		{
			// Port 1 is reserved and nothing listens there: the connection is refused at once.
			name:     "unreachable",
			testURL:  ptr("postgres://127.0.0.1:1/grimoire_test?sslmode=disable&connect_timeout=2"),
			wantFail: "TEST_DATABASE_URL: server unreachable",
		},
		{
			name:     "unreachable, DATABASE_URL set",
			testURL:  ptr("postgres://127.0.0.1:1/grimoire_test?sslmode=disable&connect_timeout=2"),
			appURL:   "postgres://dev@127.0.0.1:1/grimoire?sslmode=disable",
			wantFail: "TEST_DATABASE_URL: server unreachable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setenv(t, EnvURL, tt.testURL)
			t.Setenv("DATABASE_URL", tt.appURL)
			r := runOpen()
			if tt.wantSkip != "" {
				if r.failed != "" {
					t.Fatalf("Open failed instead of skipping: %s", r.failed)
				}
				if !strings.Contains(r.skipped, tt.wantSkip) {
					t.Errorf("skip message = %q, want it to contain %q", r.skipped, tt.wantSkip)
				}
				return
			}
			if r.skipped != "" {
				t.Fatalf("Open skipped instead of failing: %s", r.skipped)
			}
			if !strings.Contains(r.failed, tt.wantFail) {
				t.Errorf("failure message = %q, want it to contain %q", r.failed, tt.wantFail)
			}
		})
	}
}

func TestConnectCase(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "no server error", err: fmt.Errorf("reach database: %w", context.DeadlineExceeded), want: "server unreachable"},
		{name: "missing database", err: wrapPg("3D000"), want: "database does not exist"},
		{name: "unknown role", err: wrapPg("28000"), want: "authentication failed"},
		{name: "wrong password", err: wrapPg("28P01"), want: "authentication failed"},
		{name: "other server error", err: wrapPg("53300"), want: "connection refused by the server"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectCase(tt.err); got != tt.want {
				t.Errorf("connectCase = %q, want %q", got, tt.want)
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
			if err == nil {
				return
			}
			for _, want := range []string{"TEST_DATABASE_URL", fmt.Sprintf("%q", tt.dbName), `"grimoire_test"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %s", err, want)
				}
			}
		})
	}
}

// The server-side cases, derived from a valid TEST_DATABASE_URL by changing
// only the database or the role. Neither connection succeeds, so nothing
// is created, altered or read on the server.
func TestConnectServerFailures(t *testing.T) {
	Open(t) // skips when TEST_DATABASE_URL is unset, fails when it is unusable
	base, err := url.Parse(strings.TrimSpace(os.Getenv(EnvURL)))
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tests := []struct {
		name   string
		modify func(u *url.URL)
		want   string
	}{
		{
			name:   "missing database",
			modify: func(u *url.URL) { u.Path = "/grimoire_test_missing_" + suffix },
			want:   "TEST_DATABASE_URL: database does not exist",
		},
		{
			name:   "unknown role",
			modify: func(u *url.URL) { u.User = url.User("grimoire_missing_role_" + suffix) },
			want:   "TEST_DATABASE_URL: authentication failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := *base
			tt.modify(&u)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := connect(ctx, u.String())
			if conn != nil {
				conn.Close()
				t.Fatal("connect succeeded, want a failure")
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
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

func ptr(s string) *string { return &s }

// setenv sets key to *v for the test, or unsets it when v is nil.
func setenv(t *testing.T, key string, v *string) {
	t.Helper()
	if v != nil {
		t.Setenv(key, *v)
		return
	}
	t.Setenv(key, "") // registers the restore of the previous value
	os.Unsetenv(key)
}

func wrapPg(code string) error {
	return fmt.Errorf("reach database: %w", &pgconn.PgError{Severity: "FATAL", Code: code})
}
