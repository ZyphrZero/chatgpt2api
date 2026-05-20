package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

const (
	authIPWindow         = 15 * time.Minute
	authLoginIPMax       = 20
	authRegisterIPMax    = 3
	turnstileVerifyURL   = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileHTTPTimeout = 8 * time.Second
)

type authIPLimiter struct {
	mu      sync.Mutex
	buckets map[string]authIPBucket
}

type authIPBucket struct {
	WindowStart time.Time
	LoginCount  int
	Register    int
}

func newAuthIPLimiter() *authIPLimiter {
	return &authIPLimiter{buckets: map[string]authIPBucket{}}
}

func (l *authIPLimiter) allow(ip, action string, limit int) bool {
	if l == nil || limit <= 0 {
		return true
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, bucket := range l.buckets {
		if now.Sub(bucket.WindowStart) > 2*authIPWindow {
			delete(l.buckets, key)
		}
	}
	bucket := l.buckets[ip]
	if bucket.WindowStart.IsZero() || now.Sub(bucket.WindowStart) >= authIPWindow {
		bucket = authIPBucket{WindowStart: now}
	}
	switch action {
	case "register":
		if bucket.Register >= limit {
			l.buckets[ip] = bucket
			return false
		}
		bucket.Register++
	default:
		if bucket.LoginCount >= limit {
			l.buckets[ip] = bucket
			return false
		}
		bucket.LoginCount++
	}
	l.buckets[ip] = bucket
	return true
}

func (a *App) requireAuthIPAllowed(w http.ResponseWriter, r *http.Request, action string) bool {
	if a.authLimiter == nil {
		return true
	}
	limit := authLoginIPMax
	if action == "register" {
		limit = authRegisterIPMax
	}
	if a.authLimiter.allow(clientIP(r), action, limit) {
		return true
	}
	util.WriteError(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
	return false
}

func (a *App) requireTurnstile(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
	if !a.config.TurnstileReady() {
		return true
	}
	token := firstNonEmpty(
		util.Clean(body["turnstile_token"]),
		util.Clean(body["cf_turnstile_response"]),
		util.Clean(body["cf-turnstile-response"]),
	)
	if token == "" {
		util.WriteError(w, http.StatusForbidden, "请先完成 Cloudflare 验证")
		return false
	}
	if err := verifyTurnstileToken(r.Context(), a.config.TurnstileSecretKey(), token, clientIP(r)); err != nil {
		util.WriteError(w, http.StatusForbidden, "Cloudflare 验证失败，请重试")
		return false
	}
	return true
}

func verifyTurnstileToken(ctx context.Context, secret, token, remoteIP string) error {
	secret = strings.TrimSpace(secret)
	token = strings.TrimSpace(token)
	if secret == "" || token == "" {
		return errors.New("turnstile secret and token are required")
	}
	values := url.Values{}
	values.Set("secret", secret)
	values.Set("response", token)
	if remoteIP = strings.TrimSpace(remoteIP); remoteIP != "" {
		values.Set("remoteip", remoteIP)
	}
	reqCtx, cancel := context.WithTimeout(ctx, turnstileHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, turnstileVerifyURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var payload struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !payload.Success {
		return errors.New("turnstile verification failed")
	}
	return nil
}
