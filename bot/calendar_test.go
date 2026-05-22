package main

import "testing"

func TestCalID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "primary"},
		{"primary", "primary"},
		{"abc@group.calendar.google.com", "abc@group.calendar.google.com"},
	}
	for _, c := range cases {
		if got := calID(c.in); got != c.want {
			t.Errorf("calID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
