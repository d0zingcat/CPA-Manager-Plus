package model

import (
	"net/mail"
	"strings"
	"time"
)

// HealthObservationFresh bounds replay while allowing long-running inspections.
func HealthObservationFresh(observedAtMS int64, now time.Time) bool {
	return observedAtMS > 0 && observedAtMS >= now.Add(-24*time.Hour).UnixMilli() && observedAtMS <= now.Add(time.Minute).UnixMilli()
}

// HealthObservation contains only allowlisted metadata. Never pass upstream
// response bodies, error text, tokens or webhook credentials into this model.
type HealthObservation struct {
	Provider     string `json:"provider"`
	AuthIndex    string `json:"authIndex"`
	Email        string `json:"email,omitempty"`
	Source       string `json:"source,omitempty"`
	StatusCode   int    `json:"statusCode,omitempty"`
	ReasonCode   string `json:"reasonCode,omitempty"`
	Dimension    string `json:"dimension"`
	Unhealthy    bool   `json:"unhealthy"`
	ObservedAtMS int64  `json:"observedAtMs"`
	Window       string `json:"window,omitempty"`
	ResetAtMS    int64  `json:"resetAtMs,omitempty"`
}

type HealthNotification struct {
	ID       int64
	Payload  string
	Attempts int
}

// FeishuSettings is internal storage; HTTP responses use a separate redacted view.
type FeishuSettings struct {
	Enabled       bool     `json:"enabled"`
	WebhookURL    string   `json:"webhookUrl"`
	SigningSecret string   `json:"signingSecret"`
	Events        []string `json:"events"`
	UpdatedAtMS   int64    `json:"updatedAtMs"`
}

type HealthNotificationPolicy struct {
	Enabled bool
	Events  []string
}

func HealthEvent(o HealthObservation) string {
	if o.Dimension == "test" {
		return "test"
	}
	if o.Dimension == "auth" {
		if o.Unhealthy {
			return "auth_invalid"
		}
		return "auth_recovered"
	}
	if o.Unhealthy {
		return "quota_exhausted"
	}
	return "quota_recovered"
}

// HealthAccountEmail accepts only a plain email from the existing account snapshot.
// A snapshot matching the credential file name is a display fallback, not email
// evidence, even if net/mail accepts it as an address.
func HealthAccountEmail(snapshot, fileName string) string {
	value := strings.TrimSpace(snapshot)
	if strings.EqualFold(value, strings.TrimSpace(fileName)) {
		return ""
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(value, "@") {
		return ""
	}
	return strings.ToLower(value)
}
