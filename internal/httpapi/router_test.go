package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMatchAppRoute(t *testing.T) {
	routes := []appRoute{
		exact(http.MethodGet, "/version", nil),
		exact("", "/api/settings", nil),
		subtree("/api/auth/users", nil),
		prefix("/images/", nil),
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{name: "exact method", method: http.MethodGet, path: "/version", want: "/version"},
		{name: "exact method mismatch", method: http.MethodPost, path: "/version", want: ""},
		{name: "methodless exact", method: http.MethodPost, path: "/api/settings", want: "/api/settings"},
		{name: "subtree base", method: http.MethodGet, path: "/api/auth/users", want: "/api/auth/users"},
		{name: "subtree child", method: http.MethodGet, path: "/api/auth/users/123/key", want: "/api/auth/users"},
		{name: "subtree boundary", method: http.MethodGet, path: "/api/auth/users123", want: ""},
		{name: "static prefix", method: http.MethodHead, path: "/images/2026/04/a.png", want: "/images/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route := matchAppRoute(routes, tc.method, tc.path)
			got := ""
			if route != nil {
				got = route.path
			}
			if got != tc.want {
				t.Fatalf("matchAppRoute(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestAppRouterKeepsAPIMissesOutOfSPA(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing API status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/settings", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !isSPAHTML(res.Body.String()) {
		t.Fatalf("SPA route status/body = %d %q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/linuxdo/callback", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !isSPAHTML(res.Body.String()) {
		t.Fatalf("Linuxdo frontend callback status/body = %d %q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/missing", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing auth API status = %d body = %s", res.Code, res.Body.String())
	}
}
