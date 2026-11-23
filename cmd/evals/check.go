package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/evalset"
)

// runCheck validates the eval set: every record well formed, every expected
// chunk id a chunk stored in the database. It then prints the count per
// category next to the plan. It calls neither Ollama nor a text-generation
// API.
func runCheck(ctx context.Context, file string, getenv func(string) string, stdout io.Writer) error {
	qs, err := evalset.Read(file)
	if err != nil {
		return err
	}
	dbURL, err := db.URLFromEnv(getenv)
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Open(pingCtx, dbURL)
	if err != nil {
		return explainDB(err)
	}
	defer conn.Close()
	known, err := storedIDs(ctx, conn)
	if err != nil {
		return err
	}
	if unknown := evalset.UnknownIDs(qs, known); len(unknown) > 0 {
		return fmt.Errorf("%s: %d expected chunk id(s) are not stored chunks (check them against ingest srd -dry-run): %s",
			file, len(unknown), strings.Join(unknown, ", "))
	}
	return writeCounts(stdout, file, qs)
}

// writeCounts prints the count per category next to the planned count. A
// count off plan is reported, never an error.
func writeCounts(w io.Writer, file string, qs []evalset.Question) error {
	fmt.Fprintf(w, "%s: %d question(s), every record well formed, every expected chunk id stored.\n\n", file, len(qs))
	const row = "%-12s %9s %8s  %s\n"
	fmt.Fprintf(w, "%-12s %9s %8s\n", "category", "questions", "planned")
	count := evalset.Count(qs)
	total, planned := 0, 0
	for _, c := range evalset.Categories {
		n, plan := count[c], evalset.Planned[c]
		fmt.Fprintf(w, row, c, strconv.Itoa(n), strconv.Itoa(plan), offPlan(n, plan))
		total += n
		planned += plan
	}
	_, err := fmt.Fprintf(w, row, "total", strconv.Itoa(total), strconv.Itoa(planned), offPlan(total, planned))
	return err
}

func offPlan(n, plan int) string {
	switch {
	case n < plan:
		return fmt.Sprintf("%d below plan", plan-n)
	case n > plan:
		return fmt.Sprintf("%d above plan", n-plan)
	}
	return "as planned"
}
