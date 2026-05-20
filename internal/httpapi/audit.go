package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

const maxAuditPayloadBytes = 8 * 1024

type requestIdentityContextKey struct{}

type auditResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < maxAuditPayloadBytes {
		remaining := maxAuditPayloadBytes - w.body.Len()
		if len(data) > remaining {
			_, _ = w.body.Write(data[:remaining])
		} else {
			_, _ = w.body.Write(data)
		}
	}
	return w.ResponseWriter.Write(data)
}

func (w *auditResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *auditResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (a *App) serveObservedHTTP(w http.ResponseWriter, r *http.Request, routes []appRoute) {
	if r.Method == http.MethodOptions || !isAPISpace(r.URL.Path) {
		a.serveHTTP(w, r, routes)
		return
	}

	requestArgs := captureAuditRequestArgs(r)
	recorder := &auditResponseWriter{ResponseWriter: w}
	start := time.Now()
	a.serveHTTP(recorder, r, routes)
	duration := time.Since(start)
	status := recorder.statusCode()

	a.logHTTPRequest(r, status, duration)
	a.writeAuditLog(r, recorder, status, duration, requestArgs)
}

func (a *App) logHTTPRequest(r *http.Request, status int, duration time.Duration) {
	if a.logger == nil {
		return
	}
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"duration_ms", duration.Milliseconds(),
		"ip_address", clientIP(r),
	}
	switch {
	case status >= http.StatusInternalServerError:
		a.logger.Error("http request", attrs...)
	case status >= http.StatusBadRequest:
		a.logger.Warning("http request", attrs...)
	default:
		a.logger.Info("http request", attrs...)
	}
}

func (a *App) writeAuditLog(r *http.Request, recorder *auditResponseWriter, status int, duration time.Duration, requestArgs any) {
	if a.logs == nil {
		return
	}
	detail := map[string]any{
		"method":         r.Method,
		"path":           r.URL.Path,
		"module":         inferAuditModule(r.URL.Path),
		"status":         status,
		"duration_ms":    duration.Milliseconds(),
		"ip_address":     clientIP(r),
		"user_agent":     r.UserAgent(),
		"operation_type": operationTypeForMethod(r.Method),
		"log_level":      logLevelForStatus(status),
	}
	if requestArgs != nil {
		detail["request_args"] = requestArgs
	}
	if responseBody := normalizeAuditPayload(recorder.body.Bytes()); responseBody != nil {
		detail["response_body"] = responseBody
	}
	if identity, ok := requestIdentity(r.Context()); ok {
		addIdentityLogDetail(detail, identity)
		if name := identityDisplayName(identity); name != "" {
			detail["username"] = name
		}
	} else {
		detail["username"] = "anonymous"
	}

	if err := a.logs.Add(strings.TrimSpace(r.Method+" "+r.URL.Path), detail); err != nil && a.logger != nil {
		a.logger.Error("create audit log failed", "error", err, "path", r.URL.Path, "method", r.Method)
	}
}

func withRequestIdentity(ctx context.Context, identity service.Identity) context.Context {
	return context.WithValue(ctx, requestIdentityContextKey{}, identity)
}

func requestIdentity(ctx context.Context) (service.Identity, bool) {
	identity, ok := ctx.Value(requestIdentityContextKey{}).(service.Identity)
	return identity, ok
}

func captureAuditRequestArgs(r *http.Request) any {
	if r == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		return "[multipart/form-data]"
	}
	if r.Method != http.MethodGet && r.Body != nil {
		if r.ContentLength < 0 {
			return "[body omitted: unknown size]"
		}
		if r.ContentLength > maxAuditPayloadBytes {
			return "[body omitted: too large]"
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(nil))
			return nil
		}
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		return normalizeAuditPayload(body)
	}
	if strings.TrimSpace(r.URL.RawQuery) == "" {
		return nil
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return service.SanitizeLogValue(r.URL.RawQuery)
	}
	payload := make(map[string]any, len(values))
	for key, items := range values {
		if len(items) == 1 {
			payload[key] = items[0]
			continue
		}
		payload[key] = items
	}
	return service.SanitizeLogValue(payload)
}

func normalizeAuditPayload(raw []byte) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if len(trimmed) > maxAuditPayloadBytes {
		trimmed = append([]byte(nil), trimmed[:maxAuditPayloadBytes]...)
	}
	if json.Valid(trimmed) {
		var decoded any
		if err := json.Unmarshal(trimmed, &decoded); err == nil {
			return service.SanitizeLogValue(decoded)
		}
	}
	return service.SanitizeLogValue(string(trimmed))
}

func operationTypeForMethod(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet:
		return "查询"
	case http.MethodPost:
		return "提交"
	case http.MethodPut, http.MethodPatch:
		return "更新"
	case http.MethodDelete:
		return "删除"
	default:
		return "操作"
	}
}

func logLevelForStatus(status int) string {
	switch {
	case status >= http.StatusInternalServerError:
		return "error"
	case status >= http.StatusBadRequest:
		return "warning"
	default:
		return "info"
	}
}

func inferAuditModule(path string) string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return "system"
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return parts[0]
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
		return cfIP
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return util.Clean(r.RemoteAddr)
}

func parseLogQuery(r *http.Request) (service.LogQuery, error) {
	values := r.URL.Query()
	limit, err := parseLogPageSize(values.Get("page_size"))
	if err != nil {
		return service.LogQuery{}, err
	}
	return service.LogQuery{
		Username:      strings.TrimSpace(values.Get("username")),
		Module:        strings.TrimSpace(values.Get("module")),
		Method:        strings.TrimSpace(values.Get("method")),
		Summary:       strings.TrimSpace(values.Get("summary")),
		Status:        strings.TrimSpace(values.Get("status")),
		IPAddress:     strings.TrimSpace(values.Get("ip_address")),
		OperationType: strings.TrimSpace(values.Get("operation_type")),
		LogLevel:      strings.TrimSpace(values.Get("log_level")),
		StartDate:     strings.TrimSpace(values.Get("start_date")),
		EndDate:       strings.TrimSpace(values.Get("end_date")),
		StartTime:     strings.TrimSpace(values.Get("start_time")),
		EndTime:       strings.TrimSpace(values.Get("end_time")),
		Limit:         limit,
	}, nil
}

func parseLogPageSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 200, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("page_size 参数无效")
	}
	return normalizedHTTPLogPageSize(value), nil
}

func normalizedHTTPLogPageSize(limit int) int {
	if limit <= 0 {
		return 200
	}
	if limit > 500 {
		return 500
	}
	return limit
}
