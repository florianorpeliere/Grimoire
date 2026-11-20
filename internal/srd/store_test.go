package srd_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// spell returns a chunk shaped like the chunker output for a spell.
func spell(slug, title, text string) srd.Chunk {
	return srd.Chunk{
		ID:       "spells." + slug,
		Path:     []string{"Spells", title},
		FullPath: []string{"Spells", "Spell Descriptions", title},
		Title:    title,
		Kind:     srd.KindSpell,
		Text:     text,
		Hash:     "hash of " + text,
	}
}

// These cases are refused before the database is used: conn is nil.
func TestStoreRefuses(t *testing.T) {
	tests := []struct {
		name    string
		version string
		chunks  []srd.Chunk
		wantErr error
	}{
		{name: "no chunks", version: srd.Version, chunks: nil, wantErr: srd.ErrNoChunks},
		{name: "blank version", version: " ", chunks: []srd.Chunk{spell("a", "A", "a")}, wantErr: srd.ErrNoVersion},
		{
			name:    "duplicate id",
			version: srd.Version,
			chunks:  []srd.Chunk{spell("a", "A", "a"), spell("a", "A", "b")},
			wantErr: srd.ErrDuplicateID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := srd.Store(context.Background(), nil, tt.version, tt.chunks, srd.StoreOptions{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if res != (srd.StoreResult{}) {
				t.Errorf("result = %+v, want zero", res)
			}
		})
	}
}

// row is a row of srd_chunks, without its id.
type row struct {
	Path, Title, Kind, Text, Hash, Version string
	FullPath                               string // joined with " > ", to compare rows with ==
}

func wantRows(version string, chunks []srd.Chunk) map[string]row {
	m := map[string]row{}
	for _, c := range chunks {
		m[c.ID] = row{
			Path: c.PathString(), Title: c.Title, Kind: string(c.Kind), Text: c.Text,
			Hash: c.Hash, Version: version, FullPath: strings.Join(c.FullPath, " > "),
		}
	}
	return m
}

func readRows(t *testing.T, conn *sql.DB) map[string]row {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(),
		"SELECT id, path, full_path, title, kind, text, content_hash, srd_version FROM srd_chunks")
	if err != nil {
		t.Fatalf("read srd_chunks: %v", err)
	}
	defer rows.Close()
	types := pgtype.NewMap()
	m := map[string]row{}
	for rows.Next() {
		var id string
		var r row
		var fullPath []string
		if err := rows.Scan(&id, &r.Path, types.SQLScanner(&fullPath), &r.Title, &r.Kind, &r.Text, &r.Hash, &r.Version); err != nil {
			t.Fatalf("scan srd_chunks: %v", err)
		}
		r.FullPath = strings.Join(fullPath, " > ")
		m[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read srd_chunks: %v", err)
	}
	return m
}

func TestStore(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fireball := spell("fireball", "Fireball", "A bright streak flashes from you to a point you choose.")
	shield := spell("shield", "Shield", "An imperceptible barrier of magical force protects you.")
	light := spell("light", "Light", "You touch one Large or smaller object.")
	cure := spell("cure-wounds", "Cure Wounds", "A creature you touch regains a number of Hit Points.")

	newText := shield
	newText.Text = "An invisible barrier of magical force protects you."
	newText.Hash = "hash of " + newText.Text
	newFullPath := light // same text and hash, only a heading changed
	newFullPath.FullPath = []string{"Spells", "Cantrips", "Light"}

	before := []srd.Chunk{fireball, shield, light}
	after := []srd.Chunk{newText, newFullPath, cure}
	allow := srd.StoreOptions{AllowLargeDelete: true}

	// Each step runs on the table left by the previous one. stored and
	// storedVersion are what the table must hold after the step.
	steps := []struct {
		name          string
		version       string
		chunks        []srd.Chunk
		opts          srd.StoreOptions
		want          srd.StoreResult
		wantErr       error
		stored        []srd.Chunk
		storedVersion string
	}{
		{
			name: "empty table: every chunk is inserted", version: "5.2.1",
			chunks: before, want: srd.StoreResult{Inserted: 3},
			stored: before, storedVersion: "5.2.1",
		},
		{
			name: "same input: nothing changes", version: "5.2.1",
			chunks: before, want: srd.StoreResult{Unchanged: 3},
			stored: before, storedVersion: "5.2.1",
		},
		{
			name: "deleting 1 of 3 rows without the option is refused and changes nothing", version: "5.2.1",
			chunks: after, wantErr: srd.ErrLargeDelete,
			stored: before, storedVersion: "5.2.1",
		},
		{
			name: "with the option: changed chunks updated, removed one deleted, new one inserted", version: "5.2.1",
			chunks: after, opts: allow, want: srd.StoreResult{Inserted: 1, Updated: 2, Deleted: 1},
			stored: after, storedVersion: "5.2.1",
		},
		{
			name: "new SRD version: every row is updated", version: "5.2.2",
			chunks: after, want: srd.StoreResult{Updated: 3},
			stored: after, storedVersion: "5.2.2",
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			got, err := srd.Store(ctx, conn, s.version, s.chunks, s.opts)
			if !errors.Is(err, s.wantErr) {
				t.Fatalf("Store: err = %v, want %v", err, s.wantErr)
			}
			if got != s.want {
				t.Errorf("result = %+v, want %+v", got, s.want)
			}
			checkRows(t, conn, wantRows(s.storedVersion, s.stored))
		})
	}
}

func checkRows(t *testing.T, conn *sql.DB, want map[string]row) {
	t.Helper()
	rows := readRows(t, conn)
	if !maps.Equal(rows, want) {
		t.Errorf("stored ids %v, want %v", slices.Sorted(maps.Keys(rows)), slices.Sorted(maps.Keys(want)))
		for id, r := range rows {
			if r != want[id] {
				t.Errorf("row %q = %+v, want %+v", id, r, want[id])
			}
		}
	}
}

// The limit is a share of the stored rows: with 20 stored, 1 deletion (5%)
// passes and 2 (10%) need the option.
func TestStoreDeleteShare(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var all []srd.Chunk
	for i := range 20 {
		all = append(all, spell(fmt.Sprintf("spell-%02d", i), fmt.Sprintf("Spell %02d", i), fmt.Sprintf("text %d", i)))
	}

	tests := []struct {
		name    string
		remove  int
		opts    srd.StoreOptions
		wantErr error
	}{
		{name: "1 of 20 (5%)", remove: 1},
		{name: "2 of 20 (10%)", remove: 2, wantErr: srd.ErrLargeDelete},
		{name: "2 of 20 with the option", remove: 2, opts: srd.StoreOptions{AllowLargeDelete: true}},
		{name: "19 of 20 with the option", remove: 19, opts: srd.StoreOptions{AllowLargeDelete: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := srd.Store(ctx, conn, srd.Version, all, srd.StoreOptions{}); err != nil {
				t.Fatalf("store the 20 chunks: %v", err)
			}
			kept := all[:len(all)-tt.remove]
			res, err := srd.Store(ctx, conn, srd.Version, kept, tt.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				checkRows(t, conn, wantRows(srd.Version, all))
				return
			}
			if res.Deleted != tt.remove {
				t.Errorf("deleted %d, want %d", res.Deleted, tt.remove)
			}
			checkRows(t, conn, wantRows(srd.Version, kept))
		})
	}
}
