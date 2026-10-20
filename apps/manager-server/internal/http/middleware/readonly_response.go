package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const viewerResponseLimit = 32 << 20
const redacted = "[REDACTED]"

// Capture JSON reads until redaction succeeds. Raw and oversized responses fail closed.
type viewerWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *viewerWriter) Header() http.Header { return w.header }
func (w *viewerWriter) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}
func (w *viewerWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.overflow || w.body.Len()+len(b) > viewerResponseLimit {
		w.overflow = true
		return 0, errors.New("viewer response too large")
	}
	return w.body.Write(b)
}
func serveViewerRead(w http.ResponseWriter, r *http.Request, next http.Handler) {
	out := &viewerWriter{header: make(http.Header)}
	next.ServeHTTP(out, r)
	w.Header().Set("Cache-Control", "no-store")
	if out.status >= 400 {
		response.JSON(w, out.status, map[string]string{"error": "read request failed"})
		return
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(out.body.Bytes()))
	dec.UseNumber()
	var trailing any
	if out.overflow || dec.Decode(&value) != nil || dec.Decode(&trailing) != io.EOF {
		response.JSON(w, 502, map[string]string{"error": "read response unavailable"})
		return
	}
	for _, key := range []string{"X-Cpa-Version", "X-Cpa-Commit", "X-Cpa-Build-Date", "X-Cpa-Support-Plugin"} {
		if v := out.header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	switch value.(type) {
	case map[string]any, []any:
	default:
		response.JSON(w, 502, map[string]string{"error": "read response unavailable"})
		return
	}
	field := ""
	if r.URL.Path == "/v0/management/api-keys" {
		field = "apikeys"
	}
	if r.URL.Path == "/v0/management/logs" {
		field = "logs"
	}
	value = redactViewerValue(value, field)
	if strings.HasPrefix(r.URL.Path, "/v0/management/plugins/") && strings.HasSuffix(r.URL.Path, "/config") {
		// Plugins define arbitrary configuration fields: hide every string value.
		value = maskViewerStrings(value)
	}
	if r.URL.Path == "/v0/management/api-key-usage" {
		value = redactUsageMapKeys(value)
	}
	status := out.status
	if status == 0 {
		status = 200
	}
	response.JSON(w, status, value)
}
func normalizedField(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(s))
}
func sensitiveField(key string) bool {
	k := normalizedField(key)
	switch k {
	case "key", "apikey", "apikeys", "managementkey", "adminkey", "token", "accesstoken", "refreshtoken", "idtoken", "clientsecret", "secret", "secretkey", "password", "privatekey", "authorization", "cookie", "setcookie", "rawjson", "raw", "failbody", "failsummary", "errorbody", "errordetail", "errormessage", "lasterror", "statusmessage", "source", "endpoint", "rawrequest", "rawresponse", "lines", "logs", "message", "error", "details", "credential", "credentials", "datakey", "serviceaccountjson", "content":
		return true
	}
	return strings.HasSuffix(k, "apikey") || strings.HasSuffix(k, "managementkey") || strings.HasSuffix(k, "accesskey") || strings.HasSuffix(k, "password") || strings.HasSuffix(k, "secret") || strings.HasSuffix(k, "privatekey") || strings.HasSuffix(k, "accesstoken") || strings.HasSuffix(k, "refreshtoken") || strings.HasSuffix(k, "token")
}
func redactViewerValue(v any, field string) any {
	switch x := v.(type) {
	case map[string]any:
		if (sensitiveField(field) && normalizedField(field) != "apikeys") || normalizedField(field) == "headers" || normalizedField(field) == "requestheaders" || normalizedField(field) == "responseheaders" {
			for k, v := range x {
				x[k] = maskViewerStrings(v)
			}
			return x
		}
		for k, v := range x {
			x[k] = redactViewerValue(v, k)
		}
		return x
	case []any:
		for i, v := range x {
			x[i] = redactViewerValue(v, field)
		}
		return x
	case string:
		if x != "" && sensitiveField(field) {
			if normalizedField(field) == "key" || normalizedField(field) == "apikey" || normalizedField(field) == "apikeys" {
				return maskViewerKey(x)
			}
			return redacted
		}
		if u, err := url.Parse(x); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "socks5") {
			if u.User != nil {
				u.User = url.User(redacted)
			}
			q := u.Query()
			for k := range q {
				if sensitiveField(k) {
					q.Set(k, redacted)
				}
			}
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return v
}
func maskViewerStrings(v any) any {
	switch x := v.(type) {
	case string:
		if x != "" {
			return redacted
		}
	case map[string]any:
		for k, v := range x {
			x[k] = maskViewerStrings(v)
		}
	case []any:
		for i, v := range x {
			x[i] = maskViewerStrings(v)
		}
	}
	return v
}
func maskViewerKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "[REDACTED:" + hex.EncodeToString(sum[:6]) + "]"
}
func redactUsageMapKeys(v any) any {
	// Legacy shape is base URL -> raw API key -> request counters.
	if bases, ok := v.(map[string]any); ok {
		for base, raw := range bases {
			if keys, ok := raw.(map[string]any); ok {
				safe := make(map[string]any)
				for key, entry := range keys {
					safe[maskViewerKey(key)] = redactViewerValue(entry, "")
				}
				bases[base] = safe
			}
		}
	}
	return v
}
