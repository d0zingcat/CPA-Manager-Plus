package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
)

type roleContextKey struct{}

func RequestRole(r *http.Request) adminauth.Role {
	role, _ := r.Context().Value(roleContextKey{}).(adminauth.Role)
	return role
}

// UsageViewerBoundary runs before routing, including proxy and plugin fallbacks.
// New endpoints are denied to viewers unless explicitly listed here.
func UsageViewerBoundary(auth *adminauth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		role, err := auth.Role(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeAuthorizationFailure(w, "session", err)
			return
		}
		if role == "" && strings.HasPrefix(security.ExtractBearerToken(r.Header.Get("Authorization")), "cpamp_view_") {
			response.JSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid key"})
			return
		}
		if role == adminauth.RoleUsageViewer && !ViewerRouteAllowed(r.Method, r.URL.Path) {
			response.JSON(w, http.StatusForbidden, map[string]string{"error": "read-only access is restricted", "code": "forbidden"})
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), roleContextKey{}, role))
		if role == adminauth.RoleUsageViewer {
			// All reader POSTs are queries; cap their body at the shared boundary.
			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			}
			serveViewerRead(w, r, next)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ViewerRouteAllowed is an explicit catalog of reads, including POST query APIs.
// GET endpoints that initiate OAuth, refresh credentials, or download secrets are absent.
func ViewerRouteAllowed(method, path string) bool {
	if method == http.MethodPost {
		return path == "/v0/management/monitoring/analytics" || path == "/v0/management/quota-snapshots/query" || path == "/v0/management/monitoring/account-history" || path == "/v0/management/monitoring/account-window-usage"
	}
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/models", "/v1/models", "/status", "/usage-service/info", "/usage-service/auth/session", "/usage-service/config", "/usage-service/updates", "/usage-service/account-processing-policy", "/usage-service/quota-cooldowns", "/usage-service/readonly-key",
		"/v0/management/config", "/v0/management/config.yaml", "/v0/management/api-keys", "/v0/management/auth-files", "/v0/management/auth-files/models", "/v0/management/api-key-usage", "/v0/management/oauth-excluded-models", "/v0/management/oauth-model-alias",
		"/v0/management/gemini-api-key", "/v0/management/interactions-api-key", "/v0/management/codex-api-key", "/v0/management/xai-api-key", "/v0/management/meta-api-key", "/v0/management/claude-api-key", "/v0/management/vertex-api-key", "/v0/management/openai-compatibility",
		"/v0/management/logs", "/v0/management/request-error-logs", "/v0/management/logs-max-total-size-mb", "/v0/management/force-model-prefix", "/v0/management/routing/strategy", "/v0/management/latest-version", "/v0/management/plugins", "/v0/management/plugin-store",
		"/v0/management/model-prices", "/v0/management/model-prices/runtime-models", "/v0/management/model-prices/usage-summary", "/v0/management/api-key-aliases", "/v0/management/account-action-candidates", "/v0/management/codex-inspection/runs", "/v0/management/dashboard/summary",
		"/v0/management/monitoring/header-snapshots", "/v0/management/usage/maintenance", "/v0/management/usage/archives", "/v0/management/usage/import-sessions":
		return true
	}
	if rest, ok := strings.CutPrefix(path, "/v0/management/plugins/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] == "config" {
			return true
		}
	}
	// Only single-ID detail reads; action suffixes and arbitrary proxy paths stay denied.
	for _, prefix := range []string{"/v0/management/usage/archives/", "/v0/management/usage/import-sessions/", "/v0/management/codex-inspection/runs/"} {
		if id := strings.TrimPrefix(path, prefix); id != path && id != "" && !strings.Contains(id, "/") {
			return true
		}
	}
	return false
}

func ViewerRead(r *http.Request) bool {
	return RequestRole(r) == adminauth.RoleUsageViewer && ViewerRouteAllowed(r.Method, r.URL.Path)
}

func WithViewerCORS(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteCORS(cfg, w, r); next.ServeHTTP(w, r) })
}
