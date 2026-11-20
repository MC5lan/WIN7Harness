package client

import (
	"testing"

	"multifile/config"
)

func TestNewNormalizesConfig(t *testing.T) {
	c := New(config.Config{Retries: -5, TimeoutSec: -1})
	if c.Attempts() != 1 {
		t.Fatalf("Attempts = %d, want 1", c.Attempts())
	}
	if c.Timeout() != 30 {
		t.Fatalf("Timeout = %d, want 30", c.Timeout())
	}
}
