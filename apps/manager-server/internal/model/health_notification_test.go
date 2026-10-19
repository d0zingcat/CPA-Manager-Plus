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
	for _, tc := range []struct{ input, fileName, want string }{
		{" User@Example.com ", "codex.json", "user@example.com"},
		{"", "", ""}, {"codex-account.json", "codex-account.json", ""}, {"label", "", ""},
		{"User <user@example.com>", "", ""}, {"user@example.com\nspoof", "", ""},
		{"user@example.com.json", "user@example.com.json", ""},
		{" codex-user@example.com-plus.json ", "codex-user@example.com-plus.json", ""},
		{"User@Example.com.json", "user@example.com.json", ""},
		// A suffix alone is not enough to classify an address as a file name.
		{"user@example.com.json", "different.json", "user@example.com.json"},
	} {
		if got := HealthAccountEmail(tc.input, tc.fileName); got != tc.want {
			t.Errorf("email(%q, %q) = %q, want %q", tc.input, tc.fileName, got, tc.want)
		}
	}
}
