// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"testing"
	"time"
)

func TestNewClientUsesLongerDefaultTimeout(t *testing.T) {
	c := NewClient("https://example.test/v1", "key", "model")
	if c.httpCli.Timeout != DefaultRequestTimeout {
		t.Fatalf("Timeout=%s, want %s", c.httpCli.Timeout, DefaultRequestTimeout)
	}
}

func TestNewClientWithTimeoutFallsBackWhenInvalid(t *testing.T) {
	c := NewClientWithTimeout("https://example.test/v1", "key", "model", 0)
	if c.httpCli.Timeout != DefaultRequestTimeout {
		t.Fatalf("Timeout=%s, want %s", c.httpCli.Timeout, DefaultRequestTimeout)
	}
}

func TestNewClientWithTimeoutUsesConfiguredValue(t *testing.T) {
	want := 2 * time.Minute
	c := NewClientWithTimeout("https://example.test/v1", "key", "model", want)
	if c.httpCli.Timeout != want {
		t.Fatalf("Timeout=%s, want %s", c.httpCli.Timeout, want)
	}
}
