package srd_test

import (
	"context"
	"database/sql"
	"errors"
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
			res, err := srd.Store(context.Background(), nil, tt.version, tt.chunks)
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

	// Each step runs on the table left by the previous one.
	steps := []struct {
		name    string
		version string
		chunks  []srd.Chunk
		want    srd.StoreResult
	}{
		{
			name: "empty table: every chunk is inserted", version: "5.2.1",
			chunks: []srd.Chunk{fireball, shield, light},
			want:   srd.StoreResult{Inserted: 3},
		},
		{
			name: "same input: nothing changes", version: "5.2.1",
			chunks: []srd.Chunk{fireball, shield, light},
			want:   srd.StoreResult{Unchanged: 3},
		},
		{
			name: "changed chunks are updated, a removed one is deleted, a new one inserted", version: "5.2.1",
			chunks: []srd.Chunk{newText, newFullPath, cure},
			want:   srd.StoreResult{Inserted: 1, Updated: 2, Deleted: 1},
		},
		{
			name: "new SRD version: every row is updated", version: "5.2.2",
			chunks: []srd.Chunk{newText, newFullPath, cure},
			want:   srd.StoreResult{Updated: 3},
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			got, err := srd.Store(ctx, conn, s.version, s.chunks)
			if err != nil {
				t.Fatalf("Store: %v", err)
			}
			if got != s.want {
				t.Errorf("result = %+v, want %+v", got, s.want)
			}
			rows, want := readRows(t, conn), wantRows(s.version, s.chunks)
			if !maps.Equal(rows, want) {
				t.Errorf("stored ids %v, want %v", slices.Sorted(maps.Keys(rows)), slices.Sorted(maps.Keys(want)))
				for id, r := range rows {
					if r != want[id] {
						t.Errorf("row %q = %+v, want %+v", id, r, want[id])
					}
				}
			}
		})
	}
}
