package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesEmbeddedSPA(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	res := httptest.NewRecorder()
	Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("SPA route status = %d body = %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `id="root"`) && !strings.Contains(body, "id=root") &&
		!strings.Contains(body, `id="app"`) && !strings.Contains(body, "id=app") {
		t.Fatalf("SPA route body missing app root element: %q", body)
	}
}

func TestHandlerKeepsMissingAssetsOutOfSPA(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	res := httptest.NewRecorder()
	Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d body = %s", res.Code, res.Body.String())
	}
}
