package model

import (
	"testing"
	"time"
)

func TestHealthObservationFreshBoundaries(t *testing.T) {
	now := time.UnixMilli(1800000000000)
	for _, tc := range []struct {
		offset time.Duration
		want   bool
	}{{-24 * time.Hour, true}, {-24*time.Hour - time.Millisecond, false}, {-45 * time.Minute, true}, {time.Minute, true}, {time.Minute + time.Millisecond, false}} {
		if got := HealthObservationFresh(now.Add(tc.offset).UnixMilli(), now); got != tc.want {
			t.Errorf("offset=%v fresh=%v want=%v", tc.offset, got, tc.want)
		}
	}
	if HealthObservationFresh(0, now) {
		t.Fatal("zero timestamp accepted")
	}
}

func TestHealthAccountEmail(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" User@example.com ", "User@example.com"},
		{"", ""}, {"codex-account.json", ""}, {"label", ""},
		{"User <user@example.com>", ""}, {"user@example.com\nspoof", ""},
	} {
		if got := HealthAccountEmail(tc.input); got != tc.want {
			t.Errorf("email(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
