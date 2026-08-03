package engine

import "testing"

func TestParseDockerByteValue(t *testing.T) {
	tests := map[string]int64{"0B": 0, "512kB": 512000, "1.5MB": 1500000, "2GiB": 2 << 30}
	for input, want := range tests {
		got, err := parseDockerByteValue(input)
		if err != nil || got != want {
			t.Fatalf("parse %q = %d, %v; want %d", input, got, err, want)
		}
	}
}
