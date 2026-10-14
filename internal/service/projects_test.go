package service

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	long := strings.Repeat("a", 200)
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "  Roadmap  ", want: "Roadmap"},
		{in: "x", want: "x"},
		{in: long, want: long},
		{in: "   ", wantErr: true},
		{in: "", wantErr: true},
		{in: strings.Repeat("a", 201), wantErr: true},
		{in: strings.Repeat("é", 200), want: strings.Repeat("é", 200)},
		{in: strings.Repeat("é", 201), wantErr: true},
	}
	for _, c := range cases {
		got, err := ValidateName(c.in)
		if c.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("ValidateName(len %d) err = %v, want ErrInvalid", len(c.in), err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ValidateName(len %d) = %q, %v; want %q", len(c.in), got, err, c.want)
		}
	}
}
