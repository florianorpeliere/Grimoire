package srd

import "testing"

func TestVectorLiteral(t *testing.T) {
	tests := []struct {
		name string
		v    []float32
		want string
	}{
		{name: "empty", v: nil, want: "[]"},
		{name: "integers", v: []float32{1, -2, 0}, want: "[1,-2,0]"},
		{name: "shortest float32 form", v: []float32{0.1, 3e-05, -1.5e10}, want: "[0.1,3e-05,-1.5e+10]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vectorLiteral(tt.v); got != tt.want {
				t.Errorf("vectorLiteral(%v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}
