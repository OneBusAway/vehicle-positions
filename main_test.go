package main

import "testing"

func TestEnvIntOrDefault_UsesFallbackOnMissing(t *testing.T) {
	t.Setenv("RATE_LIMIT_RPS", "")
	got := envIntOrDefault("RATE_LIMIT_RPS", 7)
	if got != 7 {
		t.Fatalf("expected fallback 7, got %d", got)
	}
}

func TestEnvIntOrDefault_UsesFallbackOnInvalid(t *testing.T) {
	t.Setenv("RATE_LIMIT_RPS", "abc")
	got := envIntOrDefault("RATE_LIMIT_RPS", 7)
	if got != 7 {
		t.Fatalf("expected fallback 7 on invalid input, got %d", got)
	}
}

func TestEnvIntOrDefault_UsesFallbackOnNonPositive(t *testing.T) {
	t.Setenv("RATE_LIMIT_RPS", "0")
	got := envIntOrDefault("RATE_LIMIT_RPS", 7)
	if got != 7 {
		t.Fatalf("expected fallback 7 on non-positive input, got %d", got)
	}
}

func TestEnvIntOrDefault_UsesEnvWhenValid(t *testing.T) {
	t.Setenv("RATE_LIMIT_RPS", "12")
	got := envIntOrDefault("RATE_LIMIT_RPS", 7)
	if got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
}
