package feishunotify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebhookValidation(t *testing.T) {
	for _, raw := range []string{"http://open.feishu.cn/open-apis/bot/v2/hook/token", "https://localhost/open-apis/bot/v2/hook/token", "https://open.feishu.cn.evil.test/open-apis/bot/v2/hook/token", "https://user@open.feishu.cn/open-apis/bot/v2/hook/token", "https://open.feishu.cn/open-apis/bot/v2/hook/token?secret=abc", "https://open.feishu.cn/open-apis/bot/v2/hook/", "https://open.feishu.cn/open-apis/bot/v2/hook/token/extra"} {
		if _, err := New(&store.Store{}, config.Config{FeishuEnabled: true, FeishuWebhookURL: raw}); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := New(&store.Store{}, config.Config{FeishuEnabled: true, FeishuWebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token"}); err != nil {
		t.Fatal(err)
	}
}

func TestSigningAndAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		body      string
		status    int
		want      string
		permanent bool
	}{
		{`{"code":0}`, 200, "", false}, {`{"StatusCode":0}`, 200, "", false},
		{`{}`, 200, "missing_acknowledgement", false}, {`broken`, 200, "invalid_response", false},
		{`{"code":19021,"msg":"secret error body"}`, 200, "feishu_19021", true},
		{`{"code":9499}`, 200, "feishu_9499", true},
		{`{"code":5000}`, 200, "feishu_5000", false},
		{`{"code":11232}`, 200, "feishu_11232", false}, {``, 429, "http_429", false},
		{``, 500, "http_500", false}, {``, 302, "http_302", true},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			s, err := New(&store.Store{}, config.Config{FeishuEnabled: true, FeishuWebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token", FeishuSigningSecret: "test-secret"})
			if err != nil {
				t.Fatal(err)
			}
			s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload struct {
					Timestamp string `json:"timestamp"`
					Sign      string `json:"sign"`
					MsgType   string `json:"msg_type"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				mac := hmac.New(sha256.New, []byte(payload.Timestamp+"\n"+"test-secret"))
				if payload.Sign != base64.StdEncoding.EncodeToString(mac.Sum(nil)) || payload.MsgType != "text" {
					t.Fatal("invalid Feishu signature/payload")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			reason, permanent := s.send(context.Background(), "test")
			if reason != tc.want || permanent != tc.permanent {
				t.Fatalf("got %q/%v want %q/%v", reason, permanent, tc.want, tc.permanent)
			}
		})
	}
}

func TestTransportFailureIsRedacted(t *testing.T) {
	s, _ := New(&store.Store{}, config.Config{FeishuEnabled: true, FeishuWebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/secret-token"})
	s.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret-token upstream raw response")
	})
	if err := s.SendTest(context.Background()); err == nil || err.Error() != "transport_error" {
		t.Fatalf("unredacted error: %v", err)
	}
	disabled, _ := New(&store.Store{}, config.Config{})
	if err := disabled.SendTest(context.Background()); err == nil {
		t.Fatal("disabled test should fail")
	}
}

func TestOutboxDeliveryAcknowledgesExactlyOneTransition(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/notify.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := New(st, config.Config{FeishuEnabled: true, FeishuWebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token"})
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	s.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		sends++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0}`))}, nil
	})
	ctx := context.Background()
	if err := st.ObserveHealth(ctx, model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := s.deliverOne(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.deliverOne(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("sent %d times", sends)
	}
}

func TestWebhookNeverFollowsRedirect(t *testing.T) {
	s, _ := New(&store.Store{}, config.Config{FeishuEnabled: true, FeishuWebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/secret-token", FeishuSigningSecret: "test-secret"})
	calls := 0
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "open.feishu.cn" {
			t.Fatal("webhook payload followed redirect")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/collect"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if reason, _ := s.send(context.Background(), "test"); reason != "http_302" || calls != 1 {
		t.Fatalf("redirect handling: %s, calls=%d", reason, calls)
	}
}

func TestMessageUsesBeijingTime(t *testing.T) {
	text := message(model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", ObservedAtMS: 1, ResetAtMS: 1})
	if strings.Count(text, "1970-01-01T08:00:00+08:00") != 2 {
		t.Fatalf("expected explicit Beijing timestamps: %s", text)
	}
}
