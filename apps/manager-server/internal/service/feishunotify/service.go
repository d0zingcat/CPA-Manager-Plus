package feishunotify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Service struct {
	mu          sync.RWMutex
	testMu      sync.Mutex
	lastTest    time.Time
	locked      bool
	settings    model.FeishuSettings
	configError string
	store       *store.Store
	enabled     bool
	webhook     string
	secret      string
	client      *http.Client
}

func New(st *store.Store, cfg config.Config) (*Service, error) {
	s := &Service{store: st, enabled: cfg.FeishuEnabled, webhook: cfg.FeishuWebhookURL, secret: cfg.FeishuSigningSecret, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	s.locked = cfg.FeishuEnvSet || cfg.FeishuEnabled || cfg.FeishuWebhookURL != "" || cfg.FeishuSigningSecret != ""
	settings := model.FeishuSettings{Events: append([]string{}, AllEvents...)}
	if !s.locked && st.Settings != nil {
		loaded, err := st.Settings.LoadFeishuSettings(context.Background())
		if err != nil {
			s.configError = "stored_notification_settings_unavailable"
			log.Print("[feishu] stored notification settings unavailable; notifications disabled")
			s.apply(settings)
			return s, nil
		}
		if loaded.UpdatedAtMS > 0 {
			settings = loaded
		}
	}
	if s.locked {
		settings.Enabled = cfg.FeishuEnabled
		settings.WebhookURL = cfg.FeishuWebhookURL
		settings.SigningSecret = cfg.FeishuSigningSecret
	}
	if (settings.Enabled || settings.WebhookURL != "") && !validWebhook(settings.WebhookURL) {
		if s.locked {
			return nil, errors.New("Feishu webhook must be https://open.feishu.cn/open-apis/bot/v2/hook/<token>")
		}
		s.configError = "stored_notification_settings_invalid"
		log.Print("[feishu] invalid stored notification settings; notifications disabled")
		settings.Enabled = false
	}
	s.apply(settings)

	return s, nil
}

func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "open.feishu.cn" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	token := strings.TrimPrefix(u.Path, "/open-apis/bot/v2/hook/")
	if token == u.Path || token == "" {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// SendTest is explicit and never runs at startup. It shares the exact sender and
// acknowledgement validation used by queued events.
func (s *Service) SendTest(ctx context.Context) error {
	s.testMu.Lock()
	defer s.testMu.Unlock()
	if !s.active() {
		return errors.New("Feishu notifications are disabled")
	}
	if !s.lastTest.IsZero() && time.Since(s.lastTest) < 5*time.Second {
		return ErrTestRateLimited
	}
	s.lastTest = time.Now()
	reason, _ := s.send(ctx, "CPA Manager Plus 飞书通知测试")
	if s.store.Settings != nil {
		if err := s.store.RecordHealthTest(ctx, reason); err != nil {
			return err
		}
	}
	if reason != "" {
		return errors.New(reason)
	}
	return nil
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.deliverOne(ctx); err != nil && ctx.Err() == nil {
			log.Print("[feishu] notification queue operation failed")
		}
	}
}

func (s *Service) deliverOne(ctx context.Context) error {
	if !s.active() {
		return nil
	}
	now := time.Now().UnixMilli()
	n, ok, err := s.store.ClaimHealthNotification(ctx, now)
	if err != nil || !ok {
		return err
	}
	var o model.HealthObservation
	reason := "invalid_payload"
	permanent := true
	if json.Unmarshal([]byte(n.Payload), &o) == nil {
		if !s.store.HealthEventEnabled(model.HealthEvent(o)) {
			reason = "event_disabled"
			permanent = true
		} else {
			reason, permanent = s.send(ctx, message(o))
		}
	}
	if reason != "" {
		log.Printf("[feishu] delivery id=%d attempt=%d reason=%s", n.ID, n.Attempts, reason)
	}
	if err := s.store.FinishHealthNotification(ctx, n, reason, permanent, time.Now().UnixMilli()); err != nil {
		return err
	}
	return s.store.PruneHealthNotifications(ctx, now)
}

func message(o model.HealthObservation) string {
	event := "额度耗尽"
	if o.Dimension == "auth" {
		event = "需要重新登录"
	}
	if !o.Unhealthy {
		if o.Dimension == "auth" {
			event = "认证已恢复"
		} else {
			event = "额度已恢复"
		}
	}
	email := model.HealthAccountEmail(o.Email)
	if email == "" {
		email = "未知（未提供邮箱）"
	}
	text := fmt.Sprintf("CPA Manager Plus｜%s\nProvider：%s\n账号邮箱：%s\n账号索引：%s", event, o.Provider, email, o.AuthIndex)
	sources := map[string]string{"usage": "请求采集", "inspection": "账号巡检", "quota_snapshot": "额度快照"}
	if source := sources[o.Source]; source != "" {
		text += "\n触发来源：" + source
	}
	if o.StatusCode >= 100 && o.StatusCode <= 599 {
		text += fmt.Sprintf("\nHTTP 状态码：%d", o.StatusCode)
	}
	if o.Unhealthy && o.Dimension == "auth" {
		reasons := map[string]string{"token_revoked": "登录令牌已被撤销或作废", "invalid_credentials": "认证凭证无效或已过期"}
		if reason := reasons[o.ReasonCode]; reason != "" {
			text += "\n失败原因：" + reason
		}
		text += "\n处理建议：在凭证管理中找到该账号并重新登录"
	}
	if o.Window != "" {
		text += "\n额度窗口：" + o.Window
	}
	text += "\n发现时间：" + time.UnixMilli(o.ObservedAtMS).In(time.FixedZone("CST", 8*60*60)).Format(time.RFC3339)
	if o.ResetAtMS > 0 {
		text += "\n预计重置：" + time.UnixMilli(o.ResetAtMS).In(time.FixedZone("CST", 8*60*60)).Format(time.RFC3339)
	}
	return text
}

// Returned reasons are fixed metadata. Never propagate URL-bearing transport
// errors or upstream response bodies into logs, queue status or API responses.
func (s *Service) send(ctx context.Context, text string) (string, bool) {
	s.mu.RLock()
	webhook, secret := s.webhook, s.secret
	s.mu.RUnlock()
	payload := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
		payload["timestamp"] = timestamp
		payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	body, _ := json.Marshal(payload)
	if len(body) > 20*1024 {
		return "payload_too_large", true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return "invalid_request", true
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "transport_error", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("http_%d", resp.StatusCode), resp.StatusCode != 408 && resp.StatusCode != 429 && resp.StatusCode < 500
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(data) > 4096 {
		return "invalid_response", false
	}
	var ack struct {
		Code       *int `json:"code"`
		LegacyCode *int `json:"StatusCode"`
	}
	if json.Unmarshal(data, &ack) != nil {
		return "invalid_response", false
	}
	code := ack.Code
	if code == nil {
		code = ack.LegacyCode
	}
	if code == nil {
		return "missing_acknowledgement", false
	}
	if *code != 0 {
		temporary := false
		switch *code {
		case 4006, 5000, 11232, 11233, 11247:
			temporary = true
		}
		return fmt.Sprintf("feishu_%d", *code), !temporary
	}
	return "", false
}
