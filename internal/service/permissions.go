package service

import (
	"sort"
	"strings"

	"chatgpt2api/internal/util"
)

type MenuPermission struct {
	ID       string           `json:"id"`
	Label    string           `json:"label"`
	Path     string           `json:"path"`
	Icon     string           `json:"icon"`
	Order    int              `json:"order"`
	Children []MenuPermission `json:"children,omitempty"`
}

type APIPermission struct {
	Key     string `json:"key"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	Subtree bool   `json:"subtree,omitempty"`
}

type PermissionSet struct {
	MenuPaths      []string `json:"menu_paths"`
	APIPermissions []string `json:"api_permissions"`
}

var fullMenuPermissions = []MenuPermission{
	{ID: "image", Label: "创作台", Path: "/image", Icon: "image", Order: 10},
	{ID: "image-manager", Label: "图片库", Path: "/image-manager", Icon: "images", Order: 20},
	{ID: "accounts", Label: "号池管理", Path: "/accounts", Icon: "wallet-cards", Order: 30},
	{ID: "register", Label: "注册机", Path: "/register", Icon: "user-plus", Order: 40},
	{ID: "subscription", Label: "订阅", Path: "/subscription", Icon: "crown", Order: 50},
	{ID: "invite", Label: "邀请中心", Path: "/invite", Icon: "gift", Order: 60},
	{ID: "users", Label: "用户管理", Path: "/users", Icon: "users", Order: 70},
	{ID: "rbac", Label: "角色权限", Path: "/rbac", Icon: "shield-check", Order: 80},
	{ID: "logs", Label: "日志管理", Path: "/logs", Icon: "scroll-text", Order: 90},
	{ID: "settings", Label: "设置", Path: "/settings", Icon: "settings", Order: 100},
}

var apiPermissionCatalog = []APIPermission{
	apiPermission("GET", "/v1/models", "模型列表", "创作", false),
	apiPermission("POST", "/v1/images/generations", "文生图", "创作", false),
	apiPermission("POST", "/v1/images/edits", "图生图", "创作", false),
	apiPermission("POST", "/v1/chat/completions", "Chat Completions", "创作", false),
	apiPermission("POST", "/v1/responses", "Responses", "创作", false),
	apiPermission("POST", "/v1/messages", "Messages", "创作", false),
	apiPermission("GET", "/api/creation-tasks", "查看创作任务", "创作", true),
	apiPermission("POST", "/api/creation-tasks", "提交/取消创作任务", "创作", true),
	apiPermission("GET", "/api/images", "查看图片库", "图片库", false),
	apiPermission("PATCH", "/api/images/visibility", "发布/收回图片", "图片库", false),
	apiPermission("DELETE", "/api/images", "删除图片", "图片库", false),
	apiPermission("POST", "/api/image-shares", "创建图片分享", "图片库", false),
	apiPermission("GET", "/api/image-shares", "查看图片分享", "图片库", false),
	apiPermission("GET", "/api/image-call-stats", "查看全站调用统计", "图片库", false),
	apiPermission("GET", "/api/invite/me", "查看我的邀请", "用户中心", false),
	apiPermission("GET", "/api/checkin/status", "查看签到状态", "用户中心", false),
	apiPermission("POST", "/api/checkin", "执行签到", "用户中心", false),
	apiPermission("GET", "/api/subscription/config", "查看订阅套餐", "订阅", false),
	apiPermission("POST", "/api/subscription/checkout", "创建订阅订单", "订阅", false),
	apiPermission("GET", "/api/subscription/order", "查看订阅订单", "订阅", false),
	apiPermission("GET", "/api/auth/users", "查看个人 API 令牌", "用户令牌", true),
	apiPermission("POST", "/api/auth/users", "创建/更新个人 API 令牌", "用户令牌", true),
	apiPermission("DELETE", "/api/auth/users", "删除个人 API 令牌", "用户令牌", true),

	apiPermission("GET", "/api/accounts", "查看号池", "号池管理", false),
	apiPermission("GET", "/api/accounts/tokens", "导出号池 Token", "号池管理", false),
	apiPermission("POST", "/api/accounts", "导入号池 Token", "号池管理", false),
	apiPermission("POST", "/api/accounts/refresh", "刷新号池", "号池管理", false),
	apiPermission("POST", "/api/accounts/update", "编辑号池账号", "号池管理", false),
	apiPermission("DELETE", "/api/accounts", "删除号池账号", "号池管理", false),
	apiPermission("GET", "/api/register", "查看注册机", "注册机", true),
	apiPermission("POST", "/api/register", "控制注册机", "注册机", true),
	apiPermission("GET", "/api/logs", "查看日志", "日志管理", false),
	apiPermission("GET", "/api/logs/governance", "查看日志治理", "日志管理", false),
	apiPermission("POST", "/api/logs/governance", "清理日志数据", "日志管理", false),
	apiPermission("GET", "/api/settings", "查看设置", "设置", false),
	apiPermission("POST", "/api/settings", "修改设置", "设置", false),
	apiPermission("POST", "/api/settings/login-page-image", "修改登录页图片", "设置", false),
	apiPermission("GET", "/api/proxy", "查看代理", "设置", true),
	apiPermission("POST", "/api/proxy", "修改/测试代理", "设置", true),
	apiPermission("GET", "/api/storage/info", "查看存储状态", "设置", false),
	apiPermission("GET", "/api/admin/system", "查看系统版本和更新", "设置", true),
	apiPermission("POST", "/api/admin/system", "执行系统更新/重启", "设置", true),
	apiPermission("GET", "/api/admin/announcements", "查看公告", "公告", true),
	apiPermission("POST", "/api/admin/announcements", "创建/修改公告", "公告", true),
	apiPermission("DELETE", "/api/admin/announcements", "删除公告", "公告", true),
	apiPermission("GET", "/api/admin/subscription", "查看订阅设置", "订阅", false),
	apiPermission("POST", "/api/admin/subscription", "修改订阅设置", "订阅", false),
	apiPermission("GET", "/api/cpa/pools", "查看 CPA 池", "导入", true),
	apiPermission("POST", "/api/cpa/pools", "修改/导入 CPA 池", "导入", true),
	apiPermission("DELETE", "/api/cpa/pools", "删除 CPA 池", "导入", true),
	apiPermission("GET", "/api/sub2api/servers", "查看 Sub2API", "导入", true),
	apiPermission("POST", "/api/sub2api/servers", "修改/导入 Sub2API", "导入", true),
	apiPermission("DELETE", "/api/sub2api/servers", "删除 Sub2API", "导入", true),
	apiPermission("GET", "/api/admin/users", "查看用户", "用户管理", true),
	apiPermission("POST", "/api/admin/users", "创建/修改用户", "用户管理", true),
	apiPermission("DELETE", "/api/admin/users", "删除用户", "用户管理", true),
	apiPermission("GET", "/api/admin/roles", "查看角色", "权限管理", true),
	apiPermission("POST", "/api/admin/roles", "创建/修改角色", "权限管理", true),
	apiPermission("DELETE", "/api/admin/roles", "删除角色", "权限管理", true),
	apiPermission("GET", "/api/admin/permissions", "查看权限目录", "权限管理", false),
}

func apiPermission(method, path, label, group string, subtree bool) APIPermission {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = normalizePermissionPath(path)
	return APIPermission{
		Key:     APIPermissionKey(method, path),
		Method:  method,
		Path:    path,
		Label:   label,
		Group:   group,
		Subtree: subtree,
	}
}

func AllMenuPermissions() []MenuPermission {
	return cloneMenuPermissions(fullMenuPermissions)
}

func AllAPIPermissions() []APIPermission {
	return append([]APIPermission(nil), apiPermissionCatalog...)
}

func FilterMenuPermissions(menuPaths []string) []MenuPermission {
	allowed := sliceSet(NormalizeMenuPermissions(menuPaths))
	return filterMenuPermissions(fullMenuPermissions, allowed)
}

func DefaultPermissionSetForRole(role string) PermissionSet {
	if role == AuthRoleAdmin {
		return PermissionSet{
			MenuPaths:      allMenuPaths(),
			APIPermissions: allAPIPermissionKeys(),
		}
	}
	return PermissionSet{
		MenuPaths: NormalizeMenuPermissions([]string{
			"/image",
			"/image-manager",
			"/subscription",
			"/invite",
		}),
		APIPermissions: NormalizeAPIPermissions([]string{
			APIPermissionKey("GET", "/v1/models"),
			APIPermissionKey("POST", "/v1/images/generations"),
			APIPermissionKey("POST", "/v1/images/edits"),
			APIPermissionKey("POST", "/v1/chat/completions"),
			APIPermissionKey("POST", "/v1/responses"),
			APIPermissionKey("POST", "/v1/messages"),
			APIPermissionKey("GET", "/api/creation-tasks"),
			APIPermissionKey("POST", "/api/creation-tasks"),
			APIPermissionKey("GET", "/api/images"),
			APIPermissionKey("PATCH", "/api/images/visibility"),
			APIPermissionKey("POST", "/api/image-shares"),
			APIPermissionKey("GET", "/api/image-shares"),
			APIPermissionKey("GET", "/api/image-call-stats"),
			APIPermissionKey("GET", "/api/invite/me"),
			APIPermissionKey("GET", "/api/checkin/status"),
			APIPermissionKey("POST", "/api/checkin"),
			APIPermissionKey("GET", "/api/subscription/config"),
			APIPermissionKey("POST", "/api/subscription/checkout"),
			APIPermissionKey("GET", "/api/subscription/order"),
			APIPermissionKey("GET", "/api/auth/users"),
			APIPermissionKey("POST", "/api/auth/users"),
			APIPermissionKey("DELETE", "/api/auth/users"),
		}),
	}
}

func NormalizeMenuPermissions(paths []string) []string {
	known := map[string]struct{}{}
	for _, path := range allMenuPaths() {
		known[path] = struct{}{}
	}
	return normalizeKnownStrings(paths, known)
}

func NormalizeAPIPermissions(keys []string) []string {
	known := map[string]struct{}{}
	for _, permission := range apiPermissionCatalog {
		known[permission.Key] = struct{}{}
	}
	out := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		normalized := normalizeAPIPermissionKey(key)
		if _, ok := known[normalized]; !ok {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func APIPermissionKey(method, path string) string {
	return strings.ToLower(strings.TrimSpace(method)) + normalizePermissionPath(path)
}

func MatchAPIPermissionKey(method, path string) (string, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = normalizePermissionPath(path)
	for _, permission := range apiPermissionCatalog {
		if permission.Method != method {
			continue
		}
		if path == permission.Path {
			return permission.Key, true
		}
	}
	for _, permission := range apiPermissionCatalog {
		if permission.Method != method || !permission.Subtree {
			continue
		}
		base := strings.TrimRight(permission.Path, "/")
		if path == base || strings.HasPrefix(path, base+"/") {
			return permission.Key, true
		}
	}
	return "", false
}

func HasAPIPermission(set PermissionSet, method, path string) bool {
	key, ok := MatchAPIPermissionKey(method, path)
	if !ok {
		return false
	}
	for _, allowed := range NormalizeAPIPermissions(set.APIPermissions) {
		if allowed == key {
			return true
		}
	}
	return false
}

func normalizePermissionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func normalizeAPIPermissionKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if strings.Contains(key, " ") {
		method, path, _ := strings.Cut(key, " ")
		return migrateAPIPermissionKey(APIPermissionKey(method, path))
	}
	for _, method := range []string{"delete", "patch", "post", "put", "get"} {
		if strings.HasPrefix(strings.ToLower(key), method+"/") {
			return migrateAPIPermissionKey(method + normalizePermissionPath(key[len(method):]))
		}
	}
	return migrateAPIPermissionKey(strings.ToLower(key))
}

func migrateAPIPermissionKey(key string) string {
	switch key {
	case APIPermissionKey("GET", "/api/image-tasks"):
		return APIPermissionKey("GET", "/api/creation-tasks")
	case APIPermissionKey("POST", "/api/image-tasks"):
		return APIPermissionKey("POST", "/api/creation-tasks")
	default:
		return key
	}
}

func allMenuPaths() []string {
	var paths []string
	walkMenuPermissions(fullMenuPermissions, func(item MenuPermission) {
		if item.Path != "" {
			paths = append(paths, item.Path)
		}
	})
	sort.Strings(paths)
	return paths
}

func allAPIPermissionKeys() []string {
	out := make([]string, 0, len(apiPermissionCatalog))
	for _, permission := range apiPermissionCatalog {
		out = append(out, permission.Key)
	}
	sort.Strings(out)
	return out
}

func normalizeKnownStrings(values []string, known map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		item := normalizePermissionPath(util.Clean(value))
		if _, ok := known[item]; !ok {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func cloneMenuPermissions(items []MenuPermission) []MenuPermission {
	out := make([]MenuPermission, 0, len(items))
	for _, item := range items {
		next := item
		next.Children = cloneMenuPermissions(item.Children)
		out = append(out, next)
	}
	return out
}

func filterMenuPermissions(items []MenuPermission, allowed map[string]struct{}) []MenuPermission {
	out := make([]MenuPermission, 0, len(items))
	for _, item := range items {
		children := filterMenuPermissions(item.Children, allowed)
		_, ok := allowed[item.Path]
		if !ok && len(children) == 0 {
			continue
		}
		next := item
		next.Children = children
		out = append(out, next)
	}
	return out
}

func walkMenuPermissions(items []MenuPermission, visit func(MenuPermission)) {
	for _, item := range items {
		visit(item)
		walkMenuPermissions(item.Children, visit)
	}
}

func sliceSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
