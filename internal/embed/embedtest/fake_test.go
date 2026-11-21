package embedtest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
)

func TestFake(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		fake    *embedtest.Fake
		texts   []string
		wantErr error
	}{
		{name: "vectors of the dimension", fake: &embedtest.Fake{Dim: 4}, texts: []string{"a", "b"}},
		{name: "text within MaxRunes", fake: &embedtest.Fake{Dim: 4, MaxRunes: 3}, texts: []string{"abc"}},
		{name: "text above MaxRunes", fake: &embedtest.Fake{Dim: 4, MaxRunes: 3}, texts: []string{"a", "abcd"}, wantErr: embed.ErrInputTooLong},
		{name: "configured error", fake: &embedtest.Fake{Dim: 4, Err: boom}, texts: []string{"a"}, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vecs, err := tt.fake.Embed(context.Background(), tt.texts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got := tt.fake.Texts(); !slices.Equal(got, tt.texts) {
				t.Errorf("recorded %q, want %q", got, tt.texts)
			}
			if err != nil {
				return
			}
			if len(vecs) != len(tt.texts) {
				t.Fatalf("got %d vectors for %d texts", len(vecs), len(tt.texts))
			}
			for _, v := range vecs {
				if len(v) != tt.fake.Dim {
					t.Errorf("vector of %d dimensions, want %d", len(v), tt.fake.Dim)
				}
			}
			again, _ := tt.fake.Embed(context.Background(), tt.texts)
			if !slices.Equal(again[0], vecs[0]) {
				t.Errorf("the same text gave two vectors")
			}
		})
	}
}
