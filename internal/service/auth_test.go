package service

import (
	"path/filepath"
	"testing"

	"chatgpt2api/internal/storage"
)

func TestAuthServiceCreateAuthenticateDisableAndDelete(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	filter := AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey}
	public, raw, err := auth.CreateAPIKey(AuthRoleUser, "绘图用户", AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if raw == "" {
		t.Fatal("CreateAPIKey() returned empty raw key")
	}
	if _, ok := public["key_hash"]; ok {
		t.Fatalf("public key item leaked key_hash: %#v", public)
	}
	if _, ok := public["key"]; ok {
		t.Fatalf("public key item leaked raw key: %#v", public)
	}

	identity := auth.Authenticate(raw)
	if identity == nil {
		t.Fatal("Authenticate(raw) returned nil")
	}
	if identity.Role != "user" || identity.Name != "绘图用户" {
		t.Fatalf("identity = %#v", identity)
	}
	if !HasAPIPermission(PermissionSet{APIPermissions: identity.APIPermissions}, "POST", "/v1/images/generations") {
		t.Fatalf("default user permissions missing image generation: %#v", identity.APIPermissions)
	}

	keyID, _ := public["id"].(string)
	revealed, found := auth.RevealKey(keyID, filter)
	if !found || revealed != raw {
		t.Fatalf("RevealKey() = %q, %v; want raw, true", revealed, found)
	}

	updated := auth.UpdateKey(keyID, map[string]any{"enabled": false}, filter)
	if updated == nil {
		t.Fatal("UpdateKey() returned nil")
	}
	if auth.Authenticate(raw) != nil {
		t.Fatal("disabled key still authenticated")
	}

	if !auth.DeleteKey(keyID, filter) {
		t.Fatal("DeleteKey() = false")
	}
	if len(auth.ListKeys(filter)) != 0 {
		t.Fatalf("ListKeys(user) after delete = %#v", auth.ListKeys(filter))
	}
}

func TestAuthServiceAssignsManagedRolesToUsers(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	user, raw, err := auth.CreateAPIKey(AuthRoleUser, "operator", AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	role, err := auth.CreateRole(map[string]any{
		"name":            "accounts viewer",
		"menu_paths":      []string{"/accounts", "/missing"},
		"api_permissions": []string{APIPermissionKey("GET", "/api/accounts"), "get/missing"},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if _, err := auth.CreateRole(map[string]any{"name": "accounts viewer"}); err == nil {
		t.Fatal("duplicate role name creation succeeded")
	}
	roleID := role["id"].(string)
	userID := user["id"].(string)
	updated := auth.UpdateUser(userID, map[string]any{"role_id": roleID})
	if updated == nil {
		t.Fatal("UpdateUser() returned nil")
	}
	if updated["role_id"] != roleID || updated["role_name"] != "accounts viewer" {
		t.Fatalf("updated role fields = %#v", updated)
	}
	identity := auth.Authenticate(raw)
	if identity == nil {
		t.Fatal("Authenticate(raw) returned nil")
	}
	if identity.RoleID != roleID || identity.RoleName != "accounts viewer" {
		t.Fatalf("identity role fields = %#v", identity)
	}
	if !HasAPIPermission(PermissionSet{APIPermissions: identity.APIPermissions}, "GET", "/api/accounts") {
		t.Fatalf("updated API permissions missing accounts read: %#v", identity.APIPermissions)
	}
	if HasAPIPermission(PermissionSet{APIPermissions: identity.APIPermissions}, "POST", "/api/accounts") {
		t.Fatalf("unexpected accounts write permission: %#v", identity.APIPermissions)
	}

	if _, err := auth.UpdateRole(roleID, map[string]any{
		"api_permissions": []string{APIPermissionKey("POST", "/api/accounts")},
	}); err != nil {
		t.Fatalf("UpdateRole() error = %v", err)
	}
	identity = auth.Authenticate(raw)
	if identity == nil || !HasAPIPermission(PermissionSet{APIPermissions: identity.APIPermissions}, "POST", "/api/accounts") {
		t.Fatalf("role update did not affect user identity: %#v", identity)
	}

	if deleted, err := auth.DeleteRole(roleID); err == nil || deleted {
		t.Fatalf("DeleteRole(in use) = %v, %v; want false and error", deleted, err)
	}
}

func TestAuthServicePasswordAccountLoginAndRoleUpdates(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	bootstrap, err := auth.EnsureBootstrapAdmin("admin", "AdminPass123!")
	if err != nil {
		t.Fatalf("EnsureBootstrapAdmin() error = %v", err)
	}
	if !bootstrap.Created || bootstrap.Generated {
		t.Fatalf("bootstrap result = %#v", bootstrap)
	}
	admin, adminRaw, err := auth.LoginPassword("admin", "AdminPass123!")
	if err != nil {
		t.Fatalf("LoginPassword(admin) error = %v", err)
	}
	if admin == nil || admin.Role != AuthRoleAdmin || adminRaw == "" {
		t.Fatalf("admin identity=%#v raw=%q", admin, adminRaw)
	}

	user, raw, err := auth.RegisterPasswordUser("alice", "Password123", "Alice")
	if err != nil {
		t.Fatalf("RegisterPasswordUser() error = %v", err)
	}
	if user == nil || user.Role != AuthRoleUser || user.RoleID != DefaultManagedRoleID || raw == "" {
		t.Fatalf("registered identity=%#v raw=%q", user, raw)
	}
	if authenticated := auth.Authenticate(raw); authenticated == nil || authenticated.ID != user.ID || authenticated.Name != "Alice" {
		t.Fatalf("Authenticate(registered) = %#v", authenticated)
	}
	if _, _, err := auth.RegisterPasswordUser("alice", "Password123", "Alice again"); err == nil {
		t.Fatal("duplicate username registration succeeded")
	}

	created, err := auth.CreatePasswordUser("bob", "Password123", "Bob", DefaultManagedRoleID, false)
	if err != nil {
		t.Fatalf("CreatePasswordUser() error = %v", err)
	}
	if created == nil || created["username"] != "bob" || created["enabled"] != false || created["has_session"] != false {
		t.Fatalf("created password user = %#v", created)
	}
	if _, _, err := auth.LoginPassword("bob", "Password123"); err == nil {
		t.Fatal("disabled admin-created password account logged in")
	}
	if _, err := auth.CreatePasswordUser("bob", "Password123", "Bob", DefaultManagedRoleID, true); err == nil {
		t.Fatal("duplicate admin-created username succeeded")
	}

	role, err := auth.CreateRole(map[string]any{
		"name":            "logs viewer",
		"menu_paths":      []string{"/logs"},
		"api_permissions": []string{APIPermissionKey("GET", "/api/logs")},
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	updated := auth.UpdateUser(user.ID, map[string]any{"role_id": role["id"]})
	if updated == nil || updated["role_id"] != role["id"] {
		t.Fatalf("UpdateUser(role) = %#v", updated)
	}
	assignedRole := findManagedRole(auth.ListRoles(), role["id"].(string))
	if assignedRole == nil || assignedRole["user_count"] != 1 {
		t.Fatalf("assigned role count = %#v", assignedRole)
	}
	if deleted, err := auth.DeleteRole(role["id"].(string)); err == nil || deleted {
		t.Fatalf("DeleteRole(password account in use) = %v, %v; want false and error", deleted, err)
	}
	identity := auth.Authenticate(raw)
	if identity == nil || identity.RoleID != role["id"] || !HasAPIPermission(PermissionSet{APIPermissions: identity.APIPermissions}, "GET", "/api/logs") {
		t.Fatalf("role-updated identity = %#v", identity)
	}

	disabled := auth.UpdateUser(user.ID, map[string]any{"enabled": false})
	if disabled == nil || disabled["enabled"] != false {
		t.Fatalf("UpdateUser(disable) = %#v", disabled)
	}
	if auth.Authenticate(raw) != nil {
		t.Fatal("disabled password account session still authenticated")
	}
	if _, _, err := auth.LoginPassword("alice", "Password123"); err == nil {
		t.Fatal("disabled password account logged in")
	}
}

func TestAuthServiceLinuxDoSessionOwnsAPIKeys(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, rawSession, err := auth.UpsertLinuxDoSession(owner)
	if err != nil || rawSession == "" {
		t.Fatalf("UpsertLinuxDoSession() raw=%q err=%v", rawSession, err)
	}
	sessionIdentity := auth.Authenticate(rawSession)
	if sessionIdentity == nil {
		t.Fatal("Authenticate(session) returned nil")
	}
	if sessionIdentity.ID != owner.ID || sessionIdentity.OwnerID != owner.ID || sessionIdentity.Provider != AuthProviderLinuxDo || sessionIdentity.Kind != AuthKindSession {
		t.Fatalf("session identity = %#v", sessionIdentity)
	}

	item, rawAPIKey, err := auth.CreateAPIKey(AuthRoleUser, "绘图 API", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(owner) error = %v", err)
	}
	if rawAPIKey == "" {
		t.Fatal("CreateAPIKey(owner) returned empty key")
	}
	apiIdentity := auth.Authenticate(rawAPIKey)
	if apiIdentity == nil {
		t.Fatal("Authenticate(api key) returned nil")
	}
	if apiIdentity.ID != owner.ID || apiIdentity.CredentialID != item["id"] || apiIdentity.CredentialName != "绘图 API" {
		t.Fatalf("api identity = %#v", apiIdentity)
	}

	ownerFilter := AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey, OwnerID: owner.ID}
	keys := auth.ListKeys(ownerFilter)
	if len(keys) != 1 || keys[0]["owner_id"] != owner.ID {
		t.Fatalf("owner scoped keys = %#v", keys)
	}
	allAPIKeys := auth.ListKeys(AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey})
	if len(allAPIKeys) != 1 {
		t.Fatalf("all API keys should exclude sessions: %#v", allAPIKeys)
	}
}

func TestAuthServiceUpsertLinuxDoSessionHonorsCreateGate(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:blocked", Name: "blocked_user", Provider: AuthProviderLinuxDo, LinuxDoLevel: "1"}
	if _, _, err := auth.UpsertLinuxDoSessionIfAllowed(owner, false); err != ErrAuthUserCreationDisabled {
		t.Fatalf("UpsertLinuxDoSessionIfAllowed(disallow new) error = %v, want %v", err, ErrAuthUserCreationDisabled)
	}
	if user := findAuthUser(auth.ListUsers(), owner.ID); user != nil {
		t.Fatalf("disallowed linuxdo session created user: %#v", user)
	}

	created, createdRaw, err := auth.UpsertLinuxDoSessionIfAllowed(owner, true)
	if err != nil || createdRaw == "" {
		t.Fatalf("UpsertLinuxDoSessionIfAllowed(allow new) raw=%q err=%v", createdRaw, err)
	}
	if created["owner_id"] != owner.ID {
		t.Fatalf("created linuxdo session = %#v", created)
	}

	next, nextRaw, err := auth.UpsertLinuxDoSessionIfAllowed(owner, false)
	if err != nil || nextRaw == "" {
		t.Fatalf("UpsertLinuxDoSessionIfAllowed(existing, disallow new) raw=%q err=%v", nextRaw, err)
	}
	if next["id"] != created["id"] {
		t.Fatalf("existing linuxdo session should be updated, created=%#v next=%#v", created, next)
	}
}

func TestAuthServiceUpsertAPIKeyForOwnerKeepsOneToken(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo, LinuxDoLevel: "3"}
	if items := auth.ListSingleAPIKeyForOwner(owner.ID); len(items) != 0 {
		t.Fatalf("new owner should start with no token, got %#v", items)
	}

	first, firstRaw, err := auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("first UpsertAPIKeyForOwner() error = %v", err)
	}
	second, secondRaw, err := auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("second UpsertAPIKeyForOwner() error = %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("upsert should keep the same item id, first=%#v second=%#v", first, second)
	}
	if firstRaw == secondRaw {
		t.Fatal("upsert should rotate the raw token")
	}
	if auth.Authenticate(firstRaw) != nil {
		t.Fatal("old owner token still authenticated after reset")
	}
	if identity := auth.Authenticate(secondRaw); identity == nil || identity.ID != owner.ID {
		t.Fatalf("new owner token identity = %#v", identity)
	}
	keys := auth.ListKeys(AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey, OwnerID: owner.ID})
	if len(keys) != 1 {
		t.Fatalf("owner should have exactly one token, got %#v", keys)
	}
}

func TestAuthServiceListSingleAPIKeyForOwnerPrunesDuplicates(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo, LinuxDoLevel: "3"}
	first, firstRaw, err := auth.CreateAPIKey(AuthRoleUser, "first", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(first) error = %v", err)
	}
	_, secondRaw, err := auth.CreateAPIKey(AuthRoleUser, "second", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(second) error = %v", err)
	}
	items := auth.ListSingleAPIKeyForOwner(owner.ID)
	if len(items) != 1 || items[0]["id"] != first["id"] {
		t.Fatalf("ListSingleAPIKeyForOwner() = %#v, want first token only", items)
	}
	if auth.Authenticate(firstRaw) == nil {
		t.Fatal("kept token should still authenticate")
	}
	if auth.Authenticate(secondRaw) != nil {
		t.Fatal("pruned duplicate token still authenticated")
	}
}

func TestAuthServiceManagedUsersGroupAndControlCredentials(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, sessionRaw, err := auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	_, ownerRaw, err := auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("UpsertAPIKeyForOwner() error = %v", err)
	}
	local, localRaw, err := auth.CreateAPIKey(AuthRoleUser, "local user", AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(local) error = %v", err)
	}

	users := auth.ListUsers()
	if len(users) != 2 {
		t.Fatalf("ListUsers() length = %d users = %#v", len(users), users)
	}
	linuxdoUser := findAuthUser(users, owner.ID)
	if linuxdoUser == nil {
		t.Fatalf("missing linuxdo user in %#v", users)
	}
	if linuxdoUser["name"] != owner.Name || linuxdoUser["provider"] != AuthProviderLinuxDo || linuxdoUser["has_session"] != true || linuxdoUser["has_api_key"] != true {
		t.Fatalf("linuxdo user = %#v", linuxdoUser)
	}
	if linuxdoUser["linuxdo_level"] != "3" {
		t.Fatalf("linuxdo level = %#v", linuxdoUser)
	}
	if _, ok := linuxdoUser["key"]; ok {
		t.Fatalf("managed user leaked key: %#v", linuxdoUser)
	}
	localID, _ := local["id"].(string)
	localUser := findAuthUser(users, localID)
	if localUser == nil || localUser["provider"] != AuthProviderLocal || localUser["has_api_key"] != true {
		t.Fatalf("local user = %#v in %#v", localUser, users)
	}

	disabled := auth.UpdateUser(owner.ID, map[string]any{"enabled": false})
	if disabled == nil || disabled["enabled"] != false {
		t.Fatalf("disabled managed user = %#v", disabled)
	}
	if auth.Authenticate(sessionRaw) != nil {
		t.Fatal("disabled linuxdo session still authenticated")
	}
	if auth.Authenticate(ownerRaw) != nil {
		t.Fatal("disabled linuxdo API key still authenticated")
	}
	if auth.Authenticate(localRaw) == nil {
		t.Fatal("disabling linuxdo user should not affect local user")
	}
	disabledSession, disabledSessionRaw, err := auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession(disabled) error = %v", err)
	}
	if disabledSession["enabled"] != false {
		t.Fatalf("disabled linuxdo login session should stay disabled: %#v", disabledSession)
	}
	if auth.Authenticate(disabledSessionRaw) != nil {
		t.Fatal("disabled linuxdo user authenticated after a new login session was issued")
	}
	sessionRaw = disabledSessionRaw

	managedUser, apiKey, rotatedRaw, found, err := auth.ResetUserAPIKey(owner.ID, "rotated")
	if err != nil || !found {
		t.Fatalf("ResetUserAPIKey(owner) found=%v err=%v", found, err)
	}
	if managedUser["id"] != owner.ID || apiKey["owner_id"] != owner.ID || rotatedRaw == "" || rotatedRaw == ownerRaw {
		t.Fatalf("ResetUserAPIKey(owner) user=%#v apiKey=%#v raw=%q old=%q", managedUser, apiKey, rotatedRaw, ownerRaw)
	}
	if auth.Authenticate(ownerRaw) != nil {
		t.Fatal("old owner API key still authenticated after managed reset")
	}
	if auth.Authenticate(rotatedRaw) != nil {
		t.Fatal("rotated owner API key should keep the disabled user state")
	}
	if auth.Authenticate(sessionRaw) != nil {
		t.Fatal("resetting API key should not re-enable disabled linuxdo session")
	}

	enabled := auth.UpdateUser(owner.ID, map[string]any{"enabled": true})
	if enabled == nil || enabled["enabled"] != true {
		t.Fatalf("enabled managed user = %#v", enabled)
	}
	if auth.Authenticate(sessionRaw) == nil {
		t.Fatal("enabled linuxdo session should authenticate")
	}
	if identity := auth.Authenticate(rotatedRaw); identity == nil || identity.ID != owner.ID {
		t.Fatalf("enabled rotated owner API identity = %#v", identity)
	}
	if auth.Authenticate(sessionRaw) == nil || auth.Authenticate(rotatedRaw) == nil {
		t.Fatal("enabled linuxdo user should authenticate with session and API key")
	}

	_, _, localRotatedRaw, found, err := auth.ResetUserAPIKey(localID, "")
	if err != nil || !found {
		t.Fatalf("ResetUserAPIKey(local) found=%v err=%v", found, err)
	}
	if localRotatedRaw == "" || localRotatedRaw == localRaw {
		t.Fatalf("local reset raw = %q old = %q", localRotatedRaw, localRaw)
	}
	if auth.Authenticate(localRaw) != nil {
		t.Fatal("old local key still authenticated after managed reset")
	}
	if identity := auth.Authenticate(localRotatedRaw); identity == nil || identity.ID != localID {
		t.Fatalf("local rotated identity = %#v", identity)
	}

	if !auth.DeleteUser(owner.ID) {
		t.Fatal("DeleteUser(owner) = false")
	}
	if auth.Authenticate(sessionRaw) != nil || auth.Authenticate(rotatedRaw) != nil {
		t.Fatal("deleted linuxdo user still authenticated")
	}
	if findAuthUser(auth.ListUsers(), owner.ID) != nil {
		t.Fatalf("deleted linuxdo user still listed: %#v", auth.ListUsers())
	}
}

func findAuthUser(users []map[string]any, id string) map[string]any {
	for _, user := range users {
		if user["id"] == id {
			return user
		}
	}
	return nil
}

func findManagedRole(roles []map[string]any, id string) map[string]any {
	for _, role := range roles {
		if role["id"] == id {
			return role
		}
	}
	return nil
}
