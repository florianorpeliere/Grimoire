package search

import "testing"

func TestCompareRank(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{a: 1, b: 2, want: -1},
		{a: 2, b: 1, want: 1},
		{a: 3, b: 3, want: 0},
		{a: 0, b: 0, want: 0},
		{a: 0, b: 5, want: 1},
		{a: 5, b: 0, want: -1},
	}
	for _, tt := range tests {
		if got := compareRank(tt.a, tt.b); got != tt.want {
			t.Errorf("compareRank(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestOptionsWithDefaults(t *testing.T) {
	tests := []struct {
		name    string
		in      Options
		want    Options
		wantErr bool
	}{
		{name: "zero takes defaults", in: Options{}, want: Options{K: 60, Candidates: 20, Limit: 8}},
		{name: "set values kept", in: Options{K: 10, Candidates: 5, Limit: 3, Prefix: true}, want: Options{K: 10, Candidates: 5, Limit: 3, Prefix: true}},
		{name: "negative k", in: Options{K: -1}, wantErr: true},
		{name: "negative candidates", in: Options{Candidates: -1}, wantErr: true},
		{name: "negative limit", in: Options{Limit: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.withDefaults()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
