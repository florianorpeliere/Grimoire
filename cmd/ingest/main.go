// Command ingest prepares and fills the Grimoire database.
//
// Usage:
//
//	ingest migrate              apply pending schema migrations to the database at DATABASE_URL
//	ingest srd -dry-run [file]  split the SRD Markdown into chunks and print a report, without the database
//
// Migrations are never applied implicitly: run "ingest migrate" explicitly.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/florianorpeliere/grimoire/internal/db"
)

const usage = `usage: ingest <command>

commands:
  migrate               apply pending schema migrations to the database at DATABASE_URL
  srd -dry-run [file]   split the SRD Markdown (default data/srd/SRD_CC_v5.2.1.md) into
                        chunks and print a report; does not touch the database
`

// errUsage marks command-line mistakes, reported with exit code 2.
var errUsage = errors.New("invalid usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ingest:", err)
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: missing command", errUsage)
	}
	switch args[0] {
	case "migrate":
		if len(args) > 1 {
			return fmt.Errorf("%w: migrate takes no arguments", errUsage)
		}
		return migrate(ctx, getenv, stdout)
	case "srd":
		return srdCommand(args[1:], stdout)
	default:
		return fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}
}

func migrate(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	url, err := db.URLFromEnv(getenv)
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Open(pingCtx, url)
	if err != nil {
		return err
	}
	defer conn.Close()

	applied, err := db.Migrate(ctx, conn)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Fprintln(stdout, "migrate: database is up to date, nothing applied")
		return nil
	}
	for _, v := range applied {
		fmt.Fprintf(stdout, "migrate: applied version %d\n", v)
	}
	return nil
}
