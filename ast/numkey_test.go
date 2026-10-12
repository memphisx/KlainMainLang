package ast

import "testing"

func TestNumberKey(t *testing.T) {
	for raw, want := range map[string]string{
		"0": "0", "16": "16", "0x10": "16", "0o17": "15", "0b101": "5", "010": "8", "1_000": "1000",
		"1.50": "1.5", "1e3": "1000", "1000000": "1000000", "123456789": "123456789",
		"1e21": "1e+21", "1.5e21": "1.5e+21", "0.000001": "0.000001", "1e-7": "1e-7",
		"0.1": "0.1", ".5": "0.5", "5.": "5", "1e999": "Infinity", "2.5e-10": "2.5e-10",
	} {
		if got := NumberKey(raw); got != want {
			t.Errorf("NumberKey(%q) = %q, want %q", raw, got, want)
		}
	}
}
