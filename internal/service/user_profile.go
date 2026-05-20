package service

import (
	"crypto/rand"
	"errors"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	userProfilesDocumentName = "user_profiles.json"
	inviteCodeAlphabet       = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	checkinCycleDays         = 7
	checkinLogLimit          = 60
)

var shanghaiTZ = time.FixedZone("Asia/Shanghai", 8*60*60)

type UserProfile struct {
	OwnerID                    string
	InviteCode                 string
	InvitedBy                  string
	ImageQuotaTotal            *int
	ImageQuotaUsed             int
	InviteRewardTotal          int
	InviteBonusTotal           int
	LastCheckinDate            string
	CheckinStreak              int
	CheckinTotal               int
	CheckinRewardTotal         int
	CheckinLog                 []map[string]any
	CreatedAt                  string
	UpdatedAt                  string
	RegistrationBonusTotal     int
	RegistrationBonusGrantedAt string
}

// RegistrationBonusGrant describes a one-time registration top-up applied to a profile.
type RegistrationBonusGrant struct {
	OwnerID    string
	Amount     int
	QuotaAfter int
	GrantedAt  string
}

type UserProfileService struct {
	mu       sync.Mutex
	store    storage.JSONDocumentBackend
	fallback string
	items    map[string]UserProfile
}

func NewUserProfileService(dataDir string, backend storage.Backend) *UserProfileService {
	s := &UserProfileService{
		store:    jsonDocumentStoreFromBackend(backend),
		fallback: dataDir + "/user_profiles.json",
		items:    map[string]UserProfile{},
	}
	s.items = normalizeUserProfiles(loadStoredJSON(s.store, userProfilesDocumentName, s.fallback))
	return s
}

func (s *UserProfileService) EnsureUser(ownerID string, quotaTotal *int) (UserProfile, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return UserProfile{}, errors.New("owner id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, changed := s.ensureUserLocked(ownerID, quotaTotal)
	if changed {
		if err := s.saveLocked(); err != nil {
			return UserProfile{}, err
		}
	}
	return profile, nil
}

func (s *UserProfileService) DeleteUser(ownerID string) error {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ownerID]; !ok {
		return nil
	}
	delete(s.items, ownerID)
	return s.saveLocked()
}

// RegistrationOutcome describes what a registration call actually granted, so callers can
// emit notifications without depending on the profile state heuristics.
type RegistrationOutcome struct {
	Profile             UserProfile
	FreeQuotaGranted    int
	InviteeBonusGranted int
	InviterID           string
	InviteRewardGranted int
	InviterQuotaAfter   int
}

func (s *UserProfileService) RegisterUser(ownerID, inviteCode string, freeQuota, inviteReward, inviteeBonus int) (UserProfile, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return UserProfile{}, errors.New("owner id is required")
	}
	if freeQuota < 0 {
		freeQuota = 0
	}
	if inviteReward < 0 {
		inviteReward = 0
	}
	if inviteeBonus < 0 {
		inviteeBonus = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ownerID]; ok {
		return UserProfile{}, errors.New("user profile already exists")
	}
	normalizedInviteCode := normalizeInviteCode(inviteCode)
	if normalizedInviteCode != "" && s.ownerByInviteCodeLocked(normalizedInviteCode) == "" {
		return UserProfile{}, errors.New("邀请码无效")
	}
	profile, _ := s.ensureUserLocked(ownerID, nil)
	s.grantRegistrationBonusLocked(&profile, freeQuota)
	s.applyInviteRelationLocked(&profile, normalizedInviteCode, inviteReward, inviteeBonus)
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	if err := s.saveLocked(); err != nil {
		return UserProfile{}, err
	}
	return profile, nil
}

// RegisterUserOutcome is the new variant of RegisterUser that surfaces the actual deltas the
// caller may need (free quota credited, invite mapping, etc.) so the HTTP layer can emit
// targeted notifications instead of guessing from the resulting profile.
func (s *UserProfileService) RegisterUserOutcome(ownerID, inviteCode string, freeQuota, inviteReward, inviteeBonus int) (RegistrationOutcome, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return RegistrationOutcome{}, errors.New("owner id is required")
	}
	if freeQuota < 0 {
		freeQuota = 0
	}
	if inviteReward < 0 {
		inviteReward = 0
	}
	if inviteeBonus < 0 {
		inviteeBonus = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ownerID]; ok {
		return RegistrationOutcome{}, errors.New("user profile already exists")
	}
	normalizedInviteCode := normalizeInviteCode(inviteCode)
	if normalizedInviteCode != "" && s.ownerByInviteCodeLocked(normalizedInviteCode) == "" {
		return RegistrationOutcome{}, errors.New("邀请码无效")
	}
	profile, _ := s.ensureUserLocked(ownerID, nil)
	freeGranted := s.grantRegistrationBonusLocked(&profile, freeQuota)
	inviteOutcome := s.applyInviteRelationLocked(&profile, normalizedInviteCode, inviteReward, inviteeBonus)
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	if err := s.saveLocked(); err != nil {
		return RegistrationOutcome{}, err
	}
	outcome := RegistrationOutcome{
		Profile:             profile,
		FreeQuotaGranted:    freeGranted,
		InviteeBonusGranted: inviteOutcome.InviteeGranted,
		InviterID:           inviteOutcome.InviterID,
		InviteRewardGranted: inviteOutcome.InviterGranted,
		InviterQuotaAfter:   inviteOutcome.InviterQuotaAfter,
	}
	return outcome, nil
}

// EnsureRegisteredUser ensures the profile exists and that registration / invite bonuses
// have been credited at least once. It is idempotent: subsequent calls are no-ops once
// RegistrationBonusGrantedAt is populated and InvitedBy has been linked.
func (s *UserProfileService) EnsureRegisteredUser(ownerID, inviteCode string, freeQuota, inviteReward, inviteeBonus int) (UserProfile, bool, error) {
	outcome, err := s.EnsureRegisteredUserOutcome(ownerID, inviteCode, freeQuota, inviteReward, inviteeBonus)
	if err != nil {
		return UserProfile{}, false, err
	}
	granted := outcome.FreeQuotaGranted > 0 || outcome.InviteeBonusGranted > 0 || outcome.InviterID != ""
	return outcome.Profile, granted, nil
}

// EnsureRegisteredUserOutcome is the variant that returns explicit deltas. Callers can rely
// on the deltas to emit notifications without inspecting the resulting profile state.
func (s *UserProfileService) EnsureRegisteredUserOutcome(ownerID, inviteCode string, freeQuota, inviteReward, inviteeBonus int) (RegistrationOutcome, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return RegistrationOutcome{}, errors.New("owner id is required")
	}
	if freeQuota < 0 {
		freeQuota = 0
	}
	if inviteReward < 0 {
		inviteReward = 0
	}
	if inviteeBonus < 0 {
		inviteeBonus = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	profile, ensuredChanged := s.ensureUserLocked(ownerID, nil)
	normalizedInviteCode := normalizeInviteCode(inviteCode)
	if normalizedInviteCode != "" && s.ownerByInviteCodeLocked(normalizedInviteCode) == "" {
		return RegistrationOutcome{}, errors.New("邀请码无效")
	}

	hadMarker := profile.RegistrationBonusGrantedAt != ""
	freeGranted := s.grantRegistrationBonusLocked(&profile, freeQuota)
	markerSet := !hadMarker && profile.RegistrationBonusGrantedAt != ""
	inviteOutcome := s.applyInviteRelationLocked(&profile, normalizedInviteCode, inviteReward, inviteeBonus)

	mutated := ensuredChanged || markerSet || freeGranted > 0 || inviteOutcome.InviterID != ""
	if mutated {
		profile.UpdatedAt = util.NowISO()
	}
	s.items[ownerID] = profile
	if mutated {
		if err := s.saveLocked(); err != nil {
			return RegistrationOutcome{}, err
		}
	}
	outcome := RegistrationOutcome{
		Profile:             profile,
		FreeQuotaGranted:    freeGranted,
		InviteeBonusGranted: inviteOutcome.InviteeGranted,
		InviterID:           inviteOutcome.InviterID,
		InviteRewardGranted: inviteOutcome.InviterGranted,
		InviterQuotaAfter:   inviteOutcome.InviterQuotaAfter,
	}
	return outcome, nil
}

// inviteRelationOutcome captures the deltas applied when linking an invite.
type inviteRelationOutcome struct {
	InviterID         string
	InviteeGranted    int
	InviterGranted    int
	InviterQuotaAfter int
}

// applyInviteRelationLocked binds the new user to an inviter exactly once and credits both
// sides. It is safe to call repeatedly: once profile.InvitedBy is set, the function returns
// the empty outcome.
func (s *UserProfileService) applyInviteRelationLocked(profile *UserProfile, inviteCode string, inviteReward, inviteeBonus int) inviteRelationOutcome {
	if profile == nil || profile.InvitedBy != "" {
		return inviteRelationOutcome{}
	}
	inviterID := s.ownerByInviteCodeLocked(inviteCode)
	if inviterID == "" || inviterID == profile.OwnerID {
		return inviteRelationOutcome{}
	}
	profile.InvitedBy = inviterID
	profile.InviteBonusTotal += inviteeBonus
	addQuota(profile, inviteeBonus)
	inviter, _ := s.ensureUserLocked(inviterID, nil)
	inviter.InviteRewardTotal += inviteReward
	addQuota(&inviter, inviteReward)
	inviter.UpdatedAt = util.NowISO()
	s.items[inviterID] = inviter
	inviterAfter := 0
	if inviter.ImageQuotaTotal != nil {
		inviterAfter = *inviter.ImageQuotaTotal
	}
	return inviteRelationOutcome{
		InviterID:         inviterID,
		InviteeGranted:    inviteeBonus,
		InviterGranted:    inviteReward,
		InviterQuotaAfter: inviterAfter,
	}
}

// grantRegistrationBonusLocked tops the profile up to freeQuota the first time it is
// invoked. RegistrationBonusGrantedAt acts as the idempotency marker; it is set even when
// the resulting delta is zero (already had enough quota), so subsequent logins do not retry.
func (s *UserProfileService) grantRegistrationBonusLocked(profile *UserProfile, freeQuota int) int {
	if profile == nil {
		return 0
	}
	if profile.RegistrationBonusGrantedAt != "" {
		return 0
	}
	if freeQuota < 0 {
		freeQuota = 0
	}
	current := 0
	if profile.ImageQuotaTotal != nil {
		current = *profile.ImageQuotaTotal
	}
	delta := freeQuota - current
	if delta < 0 {
		delta = 0
	}
	if delta > 0 {
		addQuota(profile, delta)
	}
	profile.RegistrationBonusTotal += delta
	profile.RegistrationBonusGrantedAt = util.NowISO()
	return delta
}

// BackfillRegistrationBonus iterates every existing profile and credits the registration
// bonus to anyone whose RegistrationBonusGrantedAt is empty. It is idempotent. Returns the
// list of grants that produced a non-zero credit so callers can emit notifications.
func (s *UserProfileService) BackfillRegistrationBonus(freeQuota int) ([]RegistrationBonusGrant, error) {
	if freeQuota < 0 {
		freeQuota = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := make([]RegistrationBonusGrant, 0)
	changed := false
	for ownerID, profile := range s.items {
		if profile.RegistrationBonusGrantedAt != "" {
			continue
		}
		delta := s.grantRegistrationBonusLocked(&profile, freeQuota)
		profile.UpdatedAt = util.NowISO()
		s.items[ownerID] = profile
		changed = true
		quotaAfter := 0
		if profile.ImageQuotaTotal != nil {
			quotaAfter = *profile.ImageQuotaTotal
		}
		grants = append(grants, RegistrationBonusGrant{
			OwnerID:    ownerID,
			Amount:     delta,
			QuotaAfter: quotaAfter,
			GrantedAt:  profile.RegistrationBonusGrantedAt,
		})
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].OwnerID < grants[j].OwnerID })
	return grants, nil
}

// InviteCodeOf returns the invite code minted for the supplied owner, lazily creating the
// profile so callers (e.g. share links) can rely on a stable value.
func (s *UserProfileService) InviteCodeOf(ownerID string) string {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, changed := s.ensureUserLocked(ownerID, nil)
	if changed {
		_ = s.saveLocked()
	}
	return profile.InviteCode
}

// OwnerIDByInviteCode resolves an invite code back to its owner, used when binding share
// visitors back to the inviter at signup time.
func (s *UserProfileService) OwnerIDByInviteCode(code string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ownerByInviteCodeLocked(code)
}

// ListAllOwnerIDs is used by broadcast notifications. It returns a snapshot of every owner
// id known to the profile store.
func (s *UserProfileService) ListAllOwnerIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for ownerID := range s.items {
		if ownerID == "" {
			continue
		}
		out = append(out, ownerID)
	}
	sort.Strings(out)
	return out
}

func (s *UserProfileService) PublicForUser(ownerID string) map[string]any {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, changed := s.ensureUserLocked(ownerID, nil)
	if changed {
		_ = s.saveLocked()
	}
	return publicUserProfile(profile)
}

func (s *UserProfileService) EnrichUsers(users []map[string]any) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, user := range users {
		ownerID := util.Clean(user["id"])
		if ownerID == "" {
			continue
		}
		profile, didChange := s.ensureUserLocked(ownerID, nil)
		changed = changed || didChange
		for key, value := range publicUserProfile(profile) {
			user[key] = value
		}
	}
	if changed {
		_ = s.saveLocked()
	}
	return users
}

func (s *UserProfileService) UpdateUserQuota(ownerID string, quotaTotal *int) (map[string]any, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil, errors.New("owner id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, _ := s.ensureUserLocked(ownerID, quotaTotal)
	if quotaTotal != nil {
		total := maxInt(0, *quotaTotal)
		profile.ImageQuotaTotal = &total
		if profile.ImageQuotaUsed > total {
			profile.ImageQuotaUsed = total
		}
		profile.UpdatedAt = util.NowISO()
		s.items[ownerID] = profile
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return publicUserProfile(profile), nil
}

func (s *UserProfileService) AdjustUserQuota(ownerID string, delta int) (map[string]any, error) {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return nil, errors.New("owner id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, _ := s.ensureUserLocked(ownerID, nil)
	currentTotal := profile.ImageQuotaUsed
	if profile.ImageQuotaTotal != nil {
		currentTotal = *profile.ImageQuotaTotal
	}
	total := maxInt(profile.ImageQuotaUsed, currentTotal+delta)
	profile.ImageQuotaTotal = &total
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return publicUserProfile(profile), nil
}

func (s *UserProfileService) ReserveQuota(identity Identity, count int) (func(int), error) {
	if identity.Role == AuthRoleAdmin {
		return func(int) {}, nil
	}
	ownerID := util.Clean(identity.OwnerID)
	if ownerID == "" {
		ownerID = util.Clean(identity.ID)
	}
	if ownerID == "" {
		return nil, errors.New("owner id is required")
	}
	count = maxInt(1, count)
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, _ := s.ensureUserLocked(ownerID, nil)
	if profile.ImageQuotaTotal != nil {
		remaining := maxInt(0, *profile.ImageQuotaTotal-profile.ImageQuotaUsed)
		if remaining < count {
			return nil, errors.New("no available image quota")
		}
	}
	profile.ImageQuotaUsed += count
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return func(refund int) {
		if refund <= 0 {
			return
		}
		_ = s.RefundQuota(ownerID, refund)
	}, nil
}

func (s *UserProfileService) RefundQuota(ownerID string, count int) error {
	ownerID = util.Clean(ownerID)
	if ownerID == "" || count <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.items[ownerID]
	if !ok {
		return nil
	}
	profile.ImageQuotaUsed = maxInt(0, profile.ImageQuotaUsed-count)
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	return s.saveLocked()
}

func (s *UserProfileService) InviteInfo(identity Identity, baseURL string, inviteReward, inviteeBonus int) map[string]any {
	profile := s.PublicForUser(identityScopeFromIdentity(identity))
	code := util.Clean(profile["invite_code"])
	link := strings.TrimRight(util.Clean(baseURL), "/") + "/invitation?code=" + code
	return map[string]any{
		"invite_code":           code,
		"invite_link":           link,
		"invite_reward_quota":   maxInt(0, inviteReward),
		"invitee_bonus_quota":   maxInt(0, inviteeBonus),
		"invite_reward_total":   util.ToInt(profile["invite_reward_total"], 0),
		"invite_bonus_total":    util.ToInt(profile["invite_bonus_total"], 0),
		"image_quota_total":     profile["image_quota_total"],
		"image_quota_used":      profile["image_quota_used"],
		"image_quota_remaining": profile["image_quota_remaining"],
		"invited_by":            profile["invited_by"],
		"checkin_total":         profile["checkin_total"],
		"checkin_reward_total":  profile["checkin_reward_total"],
		"last_checkin_date":     profile["last_checkin_date"],
		"checkin_streak":        profile["checkin_streak"],
	}
}

func (s *UserProfileService) InviteLanding(inviteCode, baseURL string, inviteeBonus int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	inviterID := s.ownerByInviteCodeLocked(inviteCode)
	if inviterID == "" {
		return nil
	}
	profile, changed := s.ensureUserLocked(inviterID, nil)
	if changed {
		_ = s.saveLocked()
	}
	code := profile.InviteCode
	return map[string]any{
		"invite_code":         code,
		"invite_link":         strings.TrimRight(util.Clean(baseURL), "/") + "/invitation?code=" + code,
		"invitee_bonus_quota": maxInt(0, inviteeBonus),
		"inviter_id":          inviterID,
	}
}

func (s *UserProfileService) CheckinStatus(identity Identity, rewards []int, enabled bool) map[string]any {
	rewards = normalizeCheckinRewards(rewards)
	ownerID := identityScopeFromIdentity(identity)
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, changed := s.ensureUserLocked(ownerID, nil)
	if changed {
		_ = s.saveLocked()
	}
	return checkinState(profile, rewards, enabled)
}

func (s *UserProfileService) CheckinLogs(limit int) map[string]any {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]map[string]any, 0)
	for ownerID, profile := range s.items {
		for _, entry := range profile.CheckinLog {
			item := util.CopyMap(entry)
			item["owner_id"] = ownerID
			item["invite_code"] = profile.InviteCode
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return util.Clean(items[i]["date"]) > util.Clean(items[j]["date"])
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{"items": items}
}

func (s *UserProfileService) Checkin(identity Identity, rewards []int, enabled bool) (map[string]any, error) {
	if !enabled {
		return nil, errors.New("签到功能已关闭")
	}
	rewards = normalizeCheckinRewards(rewards)
	ownerID := identityScopeFromIdentity(identity)
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, _ := s.ensureUserLocked(ownerID, nil)
	today := todayShanghai()
	if profile.LastCheckinDate == today {
		return checkinState(profile, rewards, enabled), errors.New("今日已签到")
	}
	day := nextCheckinDay(profile, today)
	reward := rewards[day-1]
	profile.LastCheckinDate = today
	profile.CheckinStreak = day
	profile.CheckinTotal++
	profile.CheckinRewardTotal += reward
	addQuota(&profile, reward)
	entry := map[string]any{"date": today, "day": day, "reward": reward}
	profile.CheckinLog = append([]map[string]any{entry}, profile.CheckinLog...)
	if len(profile.CheckinLog) > checkinLogLimit {
		profile.CheckinLog = profile.CheckinLog[:checkinLogLimit]
	}
	profile.UpdatedAt = util.NowISO()
	s.items[ownerID] = profile
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	state := checkinState(profile, rewards, enabled)
	state["reward"] = reward
	state["day"] = day
	return state, nil
}

func (s *UserProfileService) ensureUserLocked(ownerID string, quotaTotal *int) (UserProfile, bool) {
	now := util.NowISO()
	profile, ok := s.items[ownerID]
	if !ok {
		profile = UserProfile{
			OwnerID:         ownerID,
			InviteCode:      s.newInviteCodeLocked(),
			ImageQuotaTotal: normalizedQuotaPtr(quotaTotal),
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		s.items[ownerID] = profile
		return profile, true
	}
	changed := false
	if profile.InviteCode == "" {
		profile.InviteCode = s.newInviteCodeLocked()
		changed = true
	}
	if quotaTotal != nil && profile.ImageQuotaTotal == nil {
		profile.ImageQuotaTotal = normalizedQuotaPtr(quotaTotal)
		changed = true
	}
	if changed {
		profile.UpdatedAt = now
		s.items[ownerID] = profile
	}
	return profile, changed
}

func (s *UserProfileService) ownerByInviteCodeLocked(value string) string {
	code := normalizeInviteCode(value)
	if code == "" {
		return ""
	}
	for ownerID, profile := range s.items {
		if normalizeInviteCode(profile.InviteCode) == code {
			return ownerID
		}
	}
	return ""
}

func (s *UserProfileService) newInviteCodeLocked() string {
	for i := 0; i < 100; i++ {
		var b strings.Builder
		for j := 0; j < 8; j++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(inviteCodeAlphabet))))
			idx := 0
			if err == nil {
				idx = int(n.Int64())
			}
			b.WriteByte(inviteCodeAlphabet[idx%len(inviteCodeAlphabet)])
		}
		code := b.String()
		if s.ownerByInviteCodeLocked(code) == "" {
			return code
		}
	}
	return strings.ToUpper(util.NewHex(8))
}

func (s *UserProfileService) saveLocked() error {
	items := make([]map[string]any, 0, len(s.items))
	for _, profile := range s.items {
		items = append(items, storedUserProfile(profile))
	}
	sort.Slice(items, func(i, j int) bool {
		return util.Clean(items[i]["owner_id"]) < util.Clean(items[j]["owner_id"])
	})
	return saveStoredJSON(s.store, userProfilesDocumentName, s.fallback, map[string]any{"items": items})
}

func normalizeUserProfiles(raw any) map[string]UserProfile {
	out := map[string]UserProfile{}
	items := util.AsMapSlice(raw)
	if obj, ok := raw.(map[string]any); ok {
		items = util.AsMapSlice(obj["items"])
	}
	for _, item := range items {
		profile := normalizeUserProfile(item)
		if profile.OwnerID != "" {
			out[profile.OwnerID] = profile
		}
	}
	return out
}

func normalizeUserProfile(raw map[string]any) UserProfile {
	ownerID := util.Clean(raw["owner_id"])
	if ownerID == "" {
		ownerID = util.Clean(raw["id"])
	}
	created := util.Clean(raw["created_at"])
	if created == "" {
		created = util.NowISO()
	}
	updated := util.Clean(raw["updated_at"])
	if updated == "" {
		updated = created
	}
	total := normalizedQuotaPtr(raw["image_quota_total"])
	used := maxInt(0, util.ToInt(raw["image_quota_used"], 0))
	if total != nil && used > *total {
		used = *total
	}
	return UserProfile{
		OwnerID:                    ownerID,
		InviteCode:                 normalizeInviteCode(raw["invite_code"]),
		InvitedBy:                  util.Clean(raw["invited_by"]),
		ImageQuotaTotal:            total,
		ImageQuotaUsed:             used,
		InviteRewardTotal:          maxInt(0, util.ToInt(raw["invite_reward_total"], 0)),
		InviteBonusTotal:           maxInt(0, util.ToInt(raw["invite_bonus_total"], 0)),
		LastCheckinDate:            normalizeDateString(raw["last_checkin_date"]),
		CheckinStreak:              maxInt(0, util.ToInt(raw["checkin_streak"], 0)),
		CheckinTotal:               maxInt(0, util.ToInt(raw["checkin_total"], 0)),
		CheckinRewardTotal:         maxInt(0, util.ToInt(raw["checkin_reward_total"], 0)),
		CheckinLog:                 normalizeCheckinLog(raw["checkin_log"]),
		CreatedAt:                  created,
		UpdatedAt:                  updated,
		RegistrationBonusTotal:     maxInt(0, util.ToInt(raw["registration_bonus_total"], 0)),
		RegistrationBonusGrantedAt: util.Clean(raw["registration_bonus_granted_at"]),
	}
}

func publicUserProfile(profile UserProfile) map[string]any {
	var total any
	var remaining any
	if profile.ImageQuotaTotal != nil {
		total = *profile.ImageQuotaTotal
		remaining = maxInt(0, *profile.ImageQuotaTotal-profile.ImageQuotaUsed)
	}
	return map[string]any{
		"image_quota_total":             total,
		"image_quota_used":              profile.ImageQuotaUsed,
		"image_quota_remaining":         remaining,
		"invite_code":                   profile.InviteCode,
		"invited_by":                    profile.InvitedBy,
		"invite_reward_total":           profile.InviteRewardTotal,
		"invite_bonus_total":            profile.InviteBonusTotal,
		"last_checkin_date":             profile.LastCheckinDate,
		"checkin_streak":                profile.CheckinStreak,
		"checkin_total":                 profile.CheckinTotal,
		"checkin_reward_total":          profile.CheckinRewardTotal,
		"registration_bonus_total":      profile.RegistrationBonusTotal,
		"registration_bonus_granted_at": profile.RegistrationBonusGrantedAt,
	}
}

func storedUserProfile(profile UserProfile) map[string]any {
	item := publicUserProfile(profile)
	item["owner_id"] = profile.OwnerID
	item["checkin_log"] = append([]map[string]any(nil), profile.CheckinLog...)
	item["created_at"] = profile.CreatedAt
	item["updated_at"] = profile.UpdatedAt
	return item
}

func checkinState(profile UserProfile, rewards []int, enabled bool) map[string]any {
	today := todayShanghai()
	checked := profile.LastCheckinDate == today
	nextDay := nextCheckinDay(profile, today)
	logs := append([]map[string]any(nil), profile.CheckinLog...)
	return map[string]any{
		"enabled":                  enabled,
		"available_for_user":       enabled,
		"today":                    today,
		"rewards":                  rewards,
		"checked_today":            checked,
		"already_checked_in_today": checked,
		"next_day":                 nextDay,
		"next_reward":              rewards[nextDay-1],
		"last_checkin_date":        profile.LastCheckinDate,
		"checkin_streak":           profile.CheckinStreak,
		"current_streak":           profile.CheckinStreak,
		"checkin_total":            profile.CheckinTotal,
		"total_days":               profile.CheckinTotal,
		"checkin_reward_total":     profile.CheckinRewardTotal,
		"total_reward":             profile.CheckinRewardTotal,
		"checkin_log":              logs,
		"log":                      logs,
		"image_quota_total":        publicUserProfile(profile)["image_quota_total"],
		"image_quota_used":         profile.ImageQuotaUsed,
		"image_quota_remaining":    publicUserProfile(profile)["image_quota_remaining"],
	}
}

func nextCheckinDay(profile UserProfile, today string) int {
	last, ok := parseDay(profile.LastCheckinDate)
	if !ok {
		return 1
	}
	current, ok := parseDay(today)
	if !ok {
		return 1
	}
	diff := int(current.Sub(last).Hours() / 24)
	if diff == 1 {
		next := profile.CheckinStreak + 1
		if next > checkinCycleDays {
			next = 1
		}
		if next < 1 {
			next = 1
		}
		return next
	}
	if diff == 0 {
		next := profile.CheckinStreak
		if next < 1 || next > checkinCycleDays {
			next = 1
		}
		return next
	}
	return 1
}

func normalizeCheckinRewards(values []int) []int {
	defaults := []int{1, 1, 2, 2, 3, 3, 7}
	if len(values) == 0 {
		return defaults
	}
	out := make([]int, checkinCycleDays)
	hasPositiveReward := false
	for i := 0; i < checkinCycleDays; i++ {
		if i < len(values) {
			out[i] = maxInt(0, values[i])
		} else {
			out[i] = defaults[i]
		}
		if out[i] > 0 {
			hasPositiveReward = true
		}
	}
	if !hasPositiveReward {
		return defaults
	}
	return out
}

func normalizeCheckinLog(value any) []map[string]any {
	items := util.AsMapSlice(value)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		date := normalizeDateString(item["date"])
		if date == "" {
			continue
		}
		day := util.ToInt(item["day"], 1)
		if day < 1 {
			day = 1
		}
		if day > checkinCycleDays {
			day = checkinCycleDays
		}
		out = append(out, map[string]any{
			"date":   date,
			"day":    day,
			"reward": maxInt(0, util.ToInt(item["reward"], 0)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return util.Clean(out[i]["date"]) > util.Clean(out[j]["date"])
	})
	if len(out) > checkinLogLimit {
		out = out[:checkinLogLimit]
	}
	return out
}

func normalizeInviteCode(value any) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(util.Clean(value)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizedQuotaPtr(value any) *int {
	if value == nil || util.Clean(value) == "" {
		return nil
	}
	n := maxInt(0, util.ToInt(value, 0))
	return &n
}

func addQuota(profile *UserProfile, amount int) {
	if amount <= 0 {
		return
	}
	if profile.ImageQuotaTotal == nil {
		total := amount
		profile.ImageQuotaTotal = &total
		return
	}
	total := *profile.ImageQuotaTotal + amount
	profile.ImageQuotaTotal = &total
}

func normalizeDateString(value any) string {
	text := util.Clean(value)
	if len(text) >= 10 {
		text = text[:10]
	}
	if _, ok := parseDay(text); !ok {
		return ""
	}
	return text
}

func parseDay(value string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", value, shanghaiTZ)
	return t, err == nil
}

func todayShanghai() string {
	return time.Now().In(shanghaiTZ).Format("2006-01-02")
}

func identityScopeFromIdentity(identity Identity) string {
	if ownerID := util.Clean(identity.OwnerID); ownerID != "" {
		return ownerID
	}
	return util.Clean(identity.ID)
}

func (s *UserProfileService) PublicInvite(inviteCode, baseURL string, inviteeBonus int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	inviterID := s.ownerByInviteCodeLocked(inviteCode)
	if inviterID == "" {
		return nil
	}
	profile, changed := s.ensureUserLocked(inviterID, nil)
	if changed {
		_ = s.saveLocked()
	}
	code := profile.InviteCode
	return map[string]any{
		"invite_code":         code,
		"invite_url":          strings.TrimRight(util.Clean(baseURL), "/") + "/invitation?code=" + code,
		"inviter_name":        maskEmail(profile.OwnerID),
		"invitee_bonus_quota": maxInt(0, inviteeBonus),
	}
}

func (s *UserProfileService) InviteSummary(identity Identity, baseURL string, inviteReward, inviteeBonus int) map[string]any {
	ownerID := identityScopeFromIdentity(identity)
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, changed := s.ensureUserLocked(ownerID, nil)
	if changed {
		_ = s.saveLocked()
	}
	invited := make([]map[string]any, 0)
	for _, other := range s.items {
		if util.Clean(other.InvitedBy) != ownerID {
			continue
		}
		invited = append(invited, map[string]any{
			"email":       maskEmail(other.OwnerID),
			"created_at":  other.CreatedAt,
			"bonus_quota": maxInt(0, other.InviteBonusTotal),
		})
	}
	sort.Slice(invited, func(i, j int) bool {
		return util.Clean(invited[i]["created_at"]) > util.Clean(invited[j]["created_at"])
	})
	invitedCount := len(invited)
	if len(invited) > 20 {
		invited = invited[:20]
	}
	code := profile.InviteCode
	return map[string]any{
		"invite_code":         code,
		"invite_url":          strings.TrimRight(util.Clean(baseURL), "/") + "/invitation?code=" + code,
		"invited_count":       invitedCount,
		"invite_reward_quota": maxInt(0, inviteReward),
		"invitee_bonus_quota": maxInt(0, inviteeBonus),
		"invite_reward_total": profile.InviteRewardTotal,
		"invited_users":       invited,
	}
}

func (s *UserProfileService) UpdateUser(ownerID string, quotaTotal *int) (map[string]any, error) {
	return s.UpdateUserQuota(ownerID, quotaTotal)
}

func maskEmail(value any) string {
	text := strings.ToLower(util.Clean(value))
	if text == "" {
		return "DF 用户"
	}
	if !strings.Contains(text, "@") {
		if len(text) <= 4 {
			return text
		}
		return text[:2] + "***" + text[len(text)-2:]
	}
	parts := strings.SplitN(text, "@", 2)
	local := parts[0]
	domain := parts[1]
	if len(local) <= 2 {
		local = local[:1] + "***"
	} else {
		local = local[:2] + "***" + local[len(local)-1:]
	}
	return local + "@" + domain
}
