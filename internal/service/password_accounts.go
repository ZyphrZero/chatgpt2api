package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"chatgpt2api/internal/util"

	"golang.org/x/crypto/bcrypt"
)

const (
	passwordAccountsDocumentName = "auth_users.json"
	passwordSessionName          = "登录会话"
)

var accountUsernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

type PasswordAccount struct {
	ID           string
	Username     string
	Name         string
	PasswordHash string
	Role         string
	RoleID       string
	Enabled      bool
	CreatedAt    string
	UpdatedAt    string
	LastLoginAt  string
}

func (a PasswordAccount) DisplayName() string {
	if name := util.Clean(a.Name); name != "" {
		return name
	}
	if username := util.Clean(a.Username); username != "" {
		return username
	}
	return "用户"
}

func (a PasswordAccount) ManagedRoleID() string {
	if a.Role != AuthRoleUser {
		return ""
	}
	if roleID := util.Clean(a.RoleID); roleID != "" {
		return roleID
	}
	return DefaultManagedRoleID
}

type BootstrapAdminResult struct {
	Created   bool
	Generated bool
	Username  string
	Password  string
}

func (s *AuthService) EnsureBootstrapAdmin(username, password string) (BootstrapAdminResult, error) {
	username, err := normalizeAccountUsername(username)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	password = strings.TrimSpace(password)
	generated := false
	if password == "" {
		password = util.RandomTokenURL(12)
		generated = true
	}
	if err := validateAccountPassword(password); err != nil {
		return BootstrapAdminResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		if account.Role == AuthRoleAdmin {
			return BootstrapAdminResult{Username: account.Username}, nil
		}
	}
	if _, ok := passwordAccountByUsernameLocked(s.accounts, username); ok {
		return BootstrapAdminResult{}, fmt.Errorf("bootstrap admin username already exists")
	}
	hash, err := hashAccountPassword(password)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	now := util.NowISO()
	account := PasswordAccount{
		ID:           AuthRoleAdmin,
		Username:     username,
		Name:         "管理员",
		PasswordHash: hash,
		Role:         AuthRoleAdmin,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.accounts = append(s.accounts, account)
	if err := s.savePasswordAccountsLocked(); err != nil {
		return BootstrapAdminResult{}, err
	}
	return BootstrapAdminResult{Created: true, Generated: generated, Username: username, Password: password}, nil
}

func (s *AuthService) RegisterPasswordUser(username, password, name string) (*Identity, string, error) {
	username, err := normalizeAccountUsername(username)
	if err != nil {
		return nil, "", err
	}
	if err := validateAccountPassword(password); err != nil {
		return nil, "", err
	}
	name = normalizeAccountDisplayName(name, username)
	hash, err := hashAccountPassword(password)
	if err != nil {
		return nil, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := passwordAccountByUsernameLocked(s.accounts, username); ok {
		return nil, "", authError("username already exists")
	}
	now := util.NowISO()
	account := PasswordAccount{
		ID:           "user_" + util.NewHex(12),
		Username:     username,
		Name:         name,
		PasswordHash: hash,
		Role:         AuthRoleUser,
		RoleID:       DefaultManagedRoleID,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.accounts = append(s.accounts, account)
	item, raw := s.issuePasswordSessionLocked(account, now)
	if err := s.savePasswordAccountsLocked(); err != nil {
		return nil, "", err
	}
	if err := s.saveLocked(); err != nil {
		return nil, "", err
	}
	return identityForAuthItem(item), raw, nil
}

func (s *AuthService) CreatePasswordUser(username, password, name, roleID string, enabled bool) (map[string]any, error) {
	username, err := normalizeAccountUsername(username)
	if err != nil {
		return nil, err
	}
	if err := validateAccountPassword(password); err != nil {
		return nil, err
	}
	name = normalizeAccountDisplayName(name, username)
	roleID = util.Clean(roleID)
	if roleID == "" {
		roleID = DefaultManagedRoleID
	}
	hash, err := hashAccountPassword(password)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := passwordAccountByUsernameLocked(s.accounts, username); ok {
		return nil, authError("username already exists")
	}
	role, ok := managedRoleByIDLocked(s.roles, roleID)
	if !ok {
		return nil, authError("role not found")
	}
	now := util.NowISO()
	account := PasswordAccount{
		ID:           "user_" + util.NewHex(12),
		Username:     username,
		Name:         name,
		PasswordHash: hash,
		Role:         AuthRoleUser,
		RoleID:       role.ID,
		Enabled:      enabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.accounts = append(s.accounts, account)
	if err := s.savePasswordAccountsLocked(); err != nil {
		return nil, err
	}
	return managedAuthUserByIDLocked(s.items, s.roles, s.accounts, account.ID), nil
}

func (s *AuthService) LoginPassword(username, password string) (*Identity, string, error) {
	username, err := normalizeAccountUsername(username)
	if err != nil {
		return nil, "", authError("用户名或密码错误")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, account, ok := passwordAccountIndexByUsernameLocked(s.accounts, username)
	if !ok || !verifyAccountPassword(password, account.PasswordHash) {
		return nil, "", authError("用户名或密码错误")
	}
	if !account.Enabled {
		return nil, "", authError("用户已被禁用")
	}
	now := util.NowISO()
	account.LastLoginAt = now
	account.UpdatedAt = now
	s.accounts[index] = account
	item, raw := s.issuePasswordSessionLocked(account, now)
	if err := s.savePasswordAccountsLocked(); err != nil {
		return nil, "", err
	}
	if err := s.saveLocked(); err != nil {
		return nil, "", err
	}
	return identityForAuthItem(item), raw, nil
}

func (s *AuthService) UpdateProfileName(identity Identity, name string) (*Identity, error) {
	ownerID := util.Clean(identity.OwnerID)
	if ownerID == "" {
		return nil, errAuthOwnerRequired()
	}
	now := util.NowISO()

	s.mu.Lock()
	defer s.mu.Unlock()

	displayName := ""
	accountFound := false
	for index, account := range s.accounts {
		if account.ID != ownerID {
			continue
		}
		account.Name = normalizeAccountDisplayName(name, account.Username)
		account.UpdatedAt = now
		s.accounts[index] = account
		displayName = account.DisplayName()
		accountFound = true
		break
	}
	if displayName == "" {
		displayName = normalizeAccountDisplayName(name, ownerID)
	}

	changedItems := false
	for _, item := range s.items {
		if util.Clean(item["owner_id"]) != ownerID {
			continue
		}
		item["owner_name"] = displayName
		item["updated_at"] = now
		changedItems = true
	}
	if accountFound {
		s.syncPasswordAccountsToItems()
		for _, item := range s.items {
			if util.Clean(item["owner_id"]) == ownerID {
				item["updated_at"] = now
			}
		}
		if err := s.savePasswordAccountsLocked(); err != nil {
			return nil, err
		}
	}
	if changedItems {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}

	nextIdentity := identity
	nextIdentity.Name = displayName
	for _, item := range s.items {
		if util.Clean(item["id"]) == identity.CredentialID {
			if updated := identityForAuthItem(item); updated != nil {
				return updated, nil
			}
		}
	}
	return &nextIdentity, nil
}

func (s *AuthService) ChangeProfilePassword(identity Identity, currentPassword, nextPassword string) error {
	ownerID := util.Clean(identity.OwnerID)
	if ownerID == "" {
		return errAuthOwnerRequired()
	}
	if strings.TrimSpace(currentPassword) == "" {
		return authError("current password is required")
	}
	if err := validateAccountPassword(nextPassword); err != nil {
		return err
	}
	now := util.NowISO()

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, account := range s.accounts {
		if account.ID != ownerID {
			continue
		}
		if !verifyAccountPassword(currentPassword, account.PasswordHash) {
			return authError("当前密码错误")
		}
		hash, err := hashAccountPassword(nextPassword)
		if err != nil {
			return err
		}
		account.PasswordHash = hash
		account.UpdatedAt = now
		s.accounts[index] = account
		return s.savePasswordAccountsLocked()
	}
	return authError("password account not found")
}

func (s *AuthService) ResetUserPassword(id, nextPassword string) (map[string]any, error) {
	id = util.Clean(id)
	if id == "" {
		return nil, authError("user id is required")
	}
	if err := validateAccountPassword(nextPassword); err != nil {
		return nil, err
	}
	hash, err := hashAccountPassword(nextPassword)
	if err != nil {
		return nil, err
	}
	now := util.NowISO()

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for index, account := range s.accounts {
		if account.ID != id || account.Role != AuthRoleUser {
			continue
		}
		account.PasswordHash = hash
		account.UpdatedAt = now
		s.accounts[index] = account
		changed = true
		break
	}
	if !changed {
		return nil, authError("password account not found")
	}

	nextItems := s.items[:0]
	itemsChanged := false
	for _, item := range s.items {
		if util.Clean(item["kind"]) == AuthKindSession &&
			util.Clean(item["provider"]) == AuthProviderLocal &&
			util.Clean(item["owner_id"]) == id {
			itemsChanged = true
			continue
		}
		nextItems = append(nextItems, item)
	}
	s.items = nextItems
	if err := s.savePasswordAccountsLocked(); err != nil {
		return nil, err
	}
	if itemsChanged {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return managedAuthUserByIDLocked(s.items, s.roles, s.accounts, id), nil
}

func (s *AuthService) issuePasswordSessionLocked(account PasswordAccount, now string) (map[string]any, string) {
	raw := "sess-" + util.RandomTokenURL(32)
	owner := AuthOwner{
		ID:       account.ID,
		Name:     account.DisplayName(),
		Provider: AuthProviderLocal,
	}
	for index, item := range s.items {
		if util.Clean(item["kind"]) != AuthKindSession ||
			util.Clean(item["provider"]) != AuthProviderLocal ||
			util.Clean(item["owner_id"]) != account.ID {
			continue
		}
		next := util.CopyMap(item)
		next["name"] = passwordSessionName
		next["owner_name"] = account.DisplayName()
		next["username"] = account.Username
		next["key"] = raw
		next["key_hash"] = util.SHA256Hex(raw)
		next["enabled"] = account.Enabled
		next["last_used_at"] = nil
		next["updated_at"] = now
		if account.Role == AuthRoleUser {
			applyManagedRoleToAuthItem(next, roleForAccountLocked(s.roles, account))
		} else {
			next["role"] = AuthRoleAdmin
			next["role_id"] = AuthRoleAdmin
			next["role_name"] = "管理员"
			applyPermissionSet(next, DefaultPermissionSetForRole(AuthRoleAdmin))
		}
		s.items[index] = next
		return next, raw
	}

	item := newAuthItem(account.Role, AuthKindSession, passwordSessionName, owner, raw)
	item["username"] = account.Username
	item["enabled"] = account.Enabled
	item["updated_at"] = now
	if account.Role == AuthRoleUser {
		applyManagedRoleToAuthItem(item, roleForAccountLocked(s.roles, account))
	} else {
		item["role_id"] = AuthRoleAdmin
		item["role_name"] = "管理员"
	}
	s.items = append(s.items, item)
	return item, raw
}

func roleForAccountLocked(roles []ManagedRole, account PasswordAccount) ManagedRole {
	role, ok := managedRoleByIDLocked(roles, account.ManagedRoleID())
	if ok {
		return role
	}
	role, _ = managedRoleByIDLocked(roles, DefaultManagedRoleID)
	return role
}

func normalizePasswordAccounts(raw any) []PasswordAccount {
	items := util.AsMapSlice(raw)
	if obj, ok := raw.(map[string]any); ok {
		items = util.AsMapSlice(obj["items"])
	}
	out := make([]PasswordAccount, 0, len(items))
	seenIDs := map[string]struct{}{}
	seenUsernames := map[string]struct{}{}
	for _, item := range items {
		account := normalizePasswordAccount(item)
		if account.ID == "" || account.Username == "" || account.PasswordHash == "" {
			continue
		}
		if _, ok := seenIDs[account.ID]; ok {
			continue
		}
		if _, ok := seenUsernames[account.Username]; ok {
			continue
		}
		seenIDs[account.ID] = struct{}{}
		seenUsernames[account.Username] = struct{}{}
		out = append(out, account)
	}
	return out
}

func normalizePasswordAccount(raw map[string]any) PasswordAccount {
	username, err := normalizeAccountUsername(util.Clean(raw["username"]))
	if err != nil {
		return PasswordAccount{}
	}
	role := normalizeAuthRole(util.Clean(raw["role"]))
	if role == "" {
		return PasswordAccount{}
	}
	id := util.Clean(raw["id"])
	if id == "" {
		return PasswordAccount{}
	}
	created := util.Clean(raw["created_at"])
	if created == "" {
		created = util.NowISO()
	}
	updated := util.Clean(raw["updated_at"])
	if updated == "" {
		updated = created
	}
	account := PasswordAccount{
		ID:           id,
		Username:     username,
		Name:         normalizeAccountDisplayName(util.Clean(raw["name"]), username),
		PasswordHash: util.Clean(raw["password_hash"]),
		Role:         role,
		RoleID:       util.Clean(raw["role_id"]),
		Enabled:      util.ToBool(util.ValueOr(raw["enabled"], true)),
		CreatedAt:    created,
		UpdatedAt:    updated,
		LastLoginAt:  util.Clean(raw["last_login_at"]),
	}
	if account.Role != AuthRoleUser {
		account.RoleID = ""
	} else if account.RoleID == "" {
		account.RoleID = DefaultManagedRoleID
	}
	return account
}

func storedPasswordAccount(account PasswordAccount) map[string]any {
	return map[string]any{
		"id":            account.ID,
		"username":      account.Username,
		"name":          account.Name,
		"password_hash": account.PasswordHash,
		"role":          account.Role,
		"role_id":       account.ManagedRoleID(),
		"enabled":       account.Enabled,
		"created_at":    account.CreatedAt,
		"updated_at":    account.UpdatedAt,
		"last_login_at": account.LastLoginAt,
	}
}

func passwordAccountByIDLocked(accounts []PasswordAccount, id string) (PasswordAccount, bool) {
	id = util.Clean(id)
	for _, account := range accounts {
		if account.ID == id {
			return account, true
		}
	}
	return PasswordAccount{}, false
}

func passwordAccountByUsernameLocked(accounts []PasswordAccount, username string) (PasswordAccount, bool) {
	_, account, ok := passwordAccountIndexByUsernameLocked(accounts, username)
	return account, ok
}

func passwordAccountIndexByUsernameLocked(accounts []PasswordAccount, username string) (int, PasswordAccount, bool) {
	username, err := normalizeAccountUsername(username)
	if err != nil {
		return -1, PasswordAccount{}, false
	}
	for index, account := range accounts {
		if account.Username == username {
			return index, account, true
		}
	}
	return -1, PasswordAccount{}, false
}

func normalizeAccountUsername(username string) (string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !accountUsernameRE.MatchString(username) {
		return "", errors.New("用户名需为 3-32 位小写字母、数字、点、下划线或短横线，并以字母或数字开头")
	}
	return username, nil
}

func normalizeAccountDisplayName(name, username string) string {
	name = util.Clean(name)
	if len([]rune(name)) > 64 {
		name = string([]rune(name)[:64])
	}
	if name != "" {
		return name
	}
	return username
}

func validateAccountPassword(password string) error {
	if len(password) < 8 {
		return errors.New("密码长度不能少于 8 位")
	}
	if len(password) > 128 {
		return errors.New("密码长度不能超过 128 位")
	}
	return nil
}

func hashAccountPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func verifyAccountPassword(password, hash string) bool {
	if password == "" || hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
