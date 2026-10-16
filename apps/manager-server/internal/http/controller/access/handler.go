package access

import (
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
)

type Handler struct{ App *app.Context }

func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}
	role, err := h.App.AdminAuthService.Role(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		failure(w)
		return
	}
	if role == "" {
		response.JSON(w, 401, map[string]string{"error": "invalid key"})
		return
	}
	capabilities := []string{"panel:read", "usage:read"}
	if role == "admin" {
		capabilities = append(capabilities, "admin")
	}
	response.JSON(w, 200, map[string]any{"role": role, "capabilities": capabilities})
}
func (h *Handler) Key(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !middleware.AuthorizeAdmin(w, r, h.App.AdminAuthService) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := h.App.AdminAuthService.ReadonlyKeyStatus(r.Context())
		if err != nil {
			failure(w)
			return
		}
		response.JSON(w, 200, status)
	case http.MethodPost:
		key, err := h.App.AdminAuthService.RotateReadonlyKey(r.Context())
		if err != nil {
			failure(w)
			return
		}
		response.JSON(w, 200, map[string]string{"key": key})
	case http.MethodDelete:
		if err := h.App.AdminAuthService.DisableReadonlyKey(r.Context()); err != nil {
			failure(w)
			return
		}
		response.JSON(w, 200, map[string]bool{"enabled": false})
	default:
		response.MethodNotAllowed(w)
	}
}
func failure(w http.ResponseWriter) {
	response.JSON(w, 500, map[string]string{"error": "request failed", "code": "request_failed"})
}
