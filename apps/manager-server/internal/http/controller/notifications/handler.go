package notifications

import (
	"encoding/json"
	"errors"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/codexinspection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/feishunotify"
	"net/http"
)

type Handler struct{ App *app.Context }

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	ok, err := h.App.AdminAuthService.VerifyHeader(r.Context(), r.Header.Get("Authorization"))
	if err != nil || !ok {
		response.Error(w, 401, errors.New("admin authentication required"))
		return
	}
	svc := h.App.FeishuNotifications
	if svc == nil {
		response.Error(w, 503, errors.New("notification settings unavailable"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	decode := func(dst any) error { d := json.NewDecoder(r.Body); d.DisallowUnknownFields(); return d.Decode(dst) }
	switch {
	case r.URL.Path == "/usage-service/notifications/feishu" && r.Method == http.MethodGet:
		value, err := svc.Status(r.Context())
		if err != nil {
			response.Error(w, 500, err)
			return
		}
		response.JSON(w, 200, value)
	case r.URL.Path == "/usage-service/notifications/feishu" && r.Method == http.MethodPatch:
		var req feishunotify.UpdateRequest
		if decode(&req) != nil {
			response.Error(w, 400, errors.New("invalid notification settings"))
			return
		}
		value, err := svc.Update(r.Context(), req)
		if err != nil {
			response.Error(w, 400, err)
			return
		}
		response.JSON(w, 200, value)
	case r.URL.Path == "/usage-service/notifications/feishu/test" && r.Method == http.MethodPost:
		if err := svc.SendTest(r.Context()); err != nil {
			status := 502
			if errors.Is(err, feishunotify.ErrTestRateLimited) {
				status = 429
			}
			response.Error(w, status, err)
			return
		}
		response.JSON(w, 200, map[string]bool{"sent": true})
	case r.URL.Path == "/usage-service/notifications/local-inspection" && r.Method == http.MethodPost:
		var req struct {
			Results []codexinspection.LocalHealthResult `json:"results"`
		}
		if decode(&req) != nil {
			response.Error(w, 400, errors.New("invalid local inspection report"))
			return
		}
		summary, err := h.App.CodexInspectionService.ReportLocalHealth(r.Context(), req.Results)
		if err != nil {
			response.Error(w, 400, err)
			return
		}
		response.JSON(w, 200, summary)
	default:
		response.MethodNotAllowed(w)
	}
}
