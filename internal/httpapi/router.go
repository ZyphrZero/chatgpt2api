package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

type routeMatch int

const (
	exactRoute routeMatch = iota
	prefixRoute
)

type appRoute struct {
	method  string
	path    string
	match   routeMatch
	handler http.HandlerFunc
}

func (a *App) Handler() http.Handler {
	routes := a.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.serveObservedHTTP(w, r, routes)
	})
}

func (a *App) routes() []appRoute {
	return []appRoute{
		exact(http.MethodGet, "/v1/models", a.handleModels),
		exact(http.MethodPost, "/v1/images/generations", a.handleImageGenerations),
		exact(http.MethodPost, "/v1/images/edits", a.handleImageEdits),
		exact(http.MethodPost, "/v1/chat/completions", a.handleChatCompletions),
		exact(http.MethodPost, "/v1/responses", a.handleResponses),
		exact(http.MethodPost, "/v1/messages", a.handleMessages),

		exact(http.MethodPost, "/auth/login", a.handleLogin),
		exact(http.MethodPost, "/auth/admin/login", a.handleAdminKeyLogin),
		exact(http.MethodPost, "/auth/logout", a.handleLogout),
		exact(http.MethodPost, "/auth/register", a.handleAccountRegister),
		exact(http.MethodGet, "/auth/session", a.handleSession),
		subtree("/auth/invite", a.handleInviteLanding),
		exact("", "/auth/providers", a.handleAuthProviders),
		exact("", "/auth/linuxdo/start", a.handleLinuxDoOAuthStart),
		exact("", "/auth/linuxdo/oauth/callback", a.handleLinuxDoOAuthCallback),
		exact(http.MethodGet, "/auth/linuxdo/callback", a.serveWeb),
		exact(http.MethodHead, "/auth/linuxdo/callback", a.serveWeb),
		exact("", "/auth/social/start", a.handleSocialLoginStart),
		exact("", "/auth/social/oauth/callback", a.handleSocialLoginCallback),
		exact(http.MethodGet, "/auth/social/callback", a.serveWeb),
		exact(http.MethodHead, "/auth/social/callback", a.serveWeb),
		exact(http.MethodGet, "/health", a.handleHealth),
		exact(http.MethodGet, "/version", func(w http.ResponseWriter, _ *http.Request) {
			util.WriteJSON(w, http.StatusOK, map[string]any{"version": version.Get()})
		}),

		exact("", "/api/announcements", a.handlePublicAnnouncements),
		subtree("/api/admin/system", a.handleAdminSystem),
		subtree("/api/admin/announcements", a.handleAdminAnnouncements),
		subtree("/api/admin/roles", a.handleAdminRoles),
		subtree("/api/admin/users", a.handleAdminUsers),
		exact("", "/api/profile", a.handleProfile),
		exact(http.MethodPost, "/api/profile/password", a.handleProfilePassword),
		subtree("/api/profile/api-key", a.handleProfileAPIKey),
		subtree("/api/profile/prompt-favorites", a.handleProfilePromptFavorites),
		subtree("/api/auth/users", a.handleUserKeys),
		subtree("/api/accounts", a.handleAccounts),
		subtree("/api/cpa/pools", a.handleCPA),
		subtree("/api/sub2api/servers", a.handleSub2API),
		subtree("/api/creation-tasks", a.handleCreationTasks),
		subtree("/api/register", a.handleRegister),
		exact("", "/api/settings", a.handleSettings),
		exact("", "/api/settings/login-page-image", a.handleLoginPageImageSettings),
		exact("", "/api/subscription/config", a.handleSubscriptionConfig),
		exact("", "/api/subscription/checkout", a.handleSubscriptionCheckout),
		exact("", "/api/subscription/order", a.handleSubscriptionOrder),
		exact(http.MethodGet, "/api/subscription/payment/qr", a.handleSubscriptionPaymentQR),
		exact("", "/api/subscription/payment/notify", a.handleSubscriptionPaymentNotify),
		exact("", "/api/subscription/payment/return", a.handleSubscriptionPaymentReturn),
		exact("", "/api/admin/subscription", a.handleAdminSubscription),
		exact(http.MethodGet, "/api/app-meta", a.handleAppMeta),
		exact(http.MethodGet, "/api/admin/permissions", a.handlePermissionCatalog),
		exact("", "/api/images/visibility", a.handleImageVisibility),
		exact("", "/api/images", a.handleImages),
		subtree("/api/image-shares", a.handleImageShares),
		subtree("/api/notifications", a.handleNotifications),
		subtree("/api/admin/notifications", a.handleAdminNotifications),
		exact(http.MethodGet, "/api/image-call-stats", a.handleImageCallStats),
		exact("", "/api/invite/me", a.handleInviteMe),
		exact("", "/api/checkin/status", a.handleCheckinStatus),
		exact("", "/api/checkin", a.handleCheckin),
		exact("", "/api/checkin/admin/config", a.handleCheckinAdminConfig),
		exact(http.MethodGet, "/api/checkin/admin/logs", a.handleCheckinAdminLogs),
		exact("", "/api/logs/governance", a.handleLogGovernance),
		exact(http.MethodGet, "/api/logs", a.handleLogs),
		exact("", "/api/proxy", a.handleProxy),
		exact("", "/api/proxy/test", a.handleProxy),
		exact(http.MethodGet, "/api/storage/info", a.handleStorageInfo),

		prefix("/images/", a.handleImageFile),
		prefix("/image-thumbnails/", a.handleImageThumbnail),
		prefix("/share-images/", a.handleShareImageFile),
		prefix("/login-page-images/", http.StripPrefix("/login-page-images/", http.FileServer(http.Dir(a.config.LoginPageImagesDir()))).ServeHTTP),
	}
}

func exact(method, path string, handler http.HandlerFunc) appRoute {
	return appRoute{method: method, path: path, match: exactRoute, handler: handler}
}

func prefix(path string, handler http.HandlerFunc) appRoute {
	return appRoute{path: path, match: prefixRoute, handler: handler}
}

func subtree(path string, handler http.HandlerFunc) appRoute {
	return prefix(path, handler)
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request, routes []appRoute) {
	applyCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if route := matchAppRoute(routes, r.Method, r.URL.Path); route != nil {
		route.handler(w, r)
		return
	}
	if isAPISpace(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	a.serveWeb(w, r)
}

func matchAppRoute(routes []appRoute, method, path string) *appRoute {
	for i := range routes {
		route := &routes[i]
		if route.method != "" && route.method != method {
			continue
		}
		switch route.match {
		case exactRoute:
			if path == route.path {
				return route
			}
		case prefixRoute:
			if path == route.path || strings.HasPrefix(path, strings.TrimRight(route.path, "/")+"/") {
				return route
			}
		}
	}
	return nil
}

func isAPISpace(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/auth" || strings.HasPrefix(path, "/auth/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" && isAllowedCredentialedOrigin(origin, r.Host) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("Access-Control-Allow-Methods", "*")
	if requestedHeaders := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); requestedHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		w.Header().Add("Vary", "Access-Control-Request-Headers")
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "*")
	}
}

func isAllowedCredentialedOrigin(origin, requestHost string) bool {
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Hostname() == "" {
		return false
	}
	requestHostname := requestHost
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHostname = host
	}
	requestHostname = strings.Trim(requestHostname, "[]")
	originHostname := originURL.Hostname()
	return strings.EqualFold(originHostname, requestHostname) ||
		isLoopbackHostname(originHostname) && isLoopbackHostname(requestHostname)
}

func isLoopbackHostname(hostname string) bool {
	switch strings.ToLower(strings.TrimSpace(hostname)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
