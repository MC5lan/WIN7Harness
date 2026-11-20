package config

import "testing"

func TestNormalizeDefaults(t *testing.T) {
	got := (Config{}).Normalize()
	if got.Retries != 1 {
		t.Fatalf("Retries = %d, want 1", got.Retries)
	}
	if got.TimeoutSec != 30 {
		t.Fatalf("TimeoutSec = %d, want 30", got.TimeoutSec)
	}
}
