package feishunotify

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var AllEvents = []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"}
var ErrTestRateLimited = errors.New("wait five seconds before sending another test notification")

type SettingsStatus struct {
	ConfigurationError      string                 `json:"configurationError,omitempty"`
	Enabled                 bool                   `json:"enabled"`
	Locked                  bool                   `json:"locked"`
	Source                  string                 `json:"source"`
	WebhookConfigured       bool                   `json:"webhookConfigured"`
	SigningSecretConfigured bool                   `json:"signingSecretConfigured"`
	Events                  []string               `json:"events"`
	UpdatedAtMS             int64                  `json:"updatedAtMs"`
	Deliveries              []store.HealthDelivery `json:"deliveries"`
}
type UpdateRequest struct {
	ReplaceConfiguration bool      `json:"replaceConfiguration"`
	Enabled              *bool     `json:"enabled"`
	WebhookURL           *string   `json:"webhookUrl"`
	SigningSecret        *string   `json:"signingSecret"`
	ClearWebhook         bool      `json:"clearWebhook"`
	ClearSigningSecret   bool      `json:"clearSigningSecret"`
	Events               *[]string `json:"events"`
}

func (s *Service) active() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.enabled }
func (s *Service) apply(cfg model.FeishuSettings) {
	s.settings = cfg
	s.enabled = cfg.Enabled
	s.webhook = cfg.WebhookURL
	s.secret = cfg.SigningSecret
	s.store.ConfigureHealthNotifications(cfg.Enabled, cfg.Events)
}
func (s *Service) Status(ctx context.Context) (SettingsStatus, error) {
	s.mu.RLock()
	cfg := s.settings
	locked := s.locked
	configError := s.configError
	s.mu.RUnlock()
	source := "database"
	if locked {
		source = "environment"
	}
	history, err := s.store.RecentHealthDeliveries(ctx)
	if err != nil {
		return SettingsStatus{}, err
	}
	return SettingsStatus{ConfigurationError: configError, Enabled: cfg.Enabled, Locked: locked, Source: source, WebhookConfigured: cfg.WebhookURL != "", SigningSecretConfigured: cfg.SigningSecret != "", Events: append([]string{}, cfg.Events...), UpdatedAtMS: cfg.UpdatedAtMS, Deliveries: history}, nil
}
func (s *Service) Update(ctx context.Context, req UpdateRequest) (SettingsStatus, error) {
	s.mu.Lock()
	if s.locked {
		s.mu.Unlock()
		return SettingsStatus{}, errors.New("notification settings are managed by environment")
	}
	unreadable := s.configError == "stored_notification_settings_unavailable"
	if unreadable && !req.ReplaceConfiguration && !req.ClearWebhook {
		s.mu.Unlock()
		return SettingsStatus{}, errors.New("restore the data key or explicitly replace the complete notification configuration")
	}
	cfg := s.settings
	if req.ReplaceConfiguration || (unreadable && req.ClearWebhook) {
		if !req.ClearWebhook && (req.Enabled == nil || req.Events == nil || req.WebhookURL == nil || (req.SigningSecret == nil && !req.ClearSigningSecret)) {
			s.mu.Unlock()
			return SettingsStatus{}, errors.New("replacement requires enabled, events, webhook and signing secret (or explicit removal)")
		}
		cfg = model.FeishuSettings{Events: append([]string{}, AllEvents...)}
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.WebhookURL != nil && strings.TrimSpace(*req.WebhookURL) != "" {
		cfg.WebhookURL = strings.TrimSpace(*req.WebhookURL)
	}
	if req.SigningSecret != nil && strings.TrimSpace(*req.SigningSecret) != "" {
		cfg.SigningSecret = strings.TrimSpace(*req.SigningSecret)
	}
	if req.ClearWebhook {
		cfg.WebhookURL = ""
		cfg.SigningSecret = ""
		cfg.Enabled = false
	}
	if req.ClearSigningSecret {
		cfg.SigningSecret = ""
	}
	if req.Events != nil {
		cfg.Events = []string{}
		seen := map[string]bool{}
		for _, event := range *req.Events {
			valid := false
			for _, known := range AllEvents {
				if event == known {
					valid = true
				}
			}
			if !valid {
				s.mu.Unlock()
				return SettingsStatus{}, errors.New("unsupported notification event")
			}
			if !seen[event] {
				cfg.Events = append(cfg.Events, event)
				seen[event] = true
			}
		}
	}
	if (cfg.Enabled || cfg.WebhookURL != "") && !validWebhook(cfg.WebhookURL) {
		s.mu.Unlock()
		return SettingsStatus{}, errors.New("invalid Feishu webhook URL")
	}
	if len(cfg.SigningSecret) > 512 {
		s.mu.Unlock()
		return SettingsStatus{}, errors.New("signing secret is too long")
	}
	cfg.UpdatedAtMS = time.Now().UnixMilli()
	if err := s.store.Settings.SaveFeishuSettings(ctx, cfg); err != nil {
		s.mu.Unlock()
		return SettingsStatus{}, err
	}
	s.apply(cfg)
	s.configError = ""
	s.mu.Unlock()
	return s.Status(ctx)
}
