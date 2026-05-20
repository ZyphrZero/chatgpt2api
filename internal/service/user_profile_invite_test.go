package service

import (
	"fmt"
	"path/filepath"
	"testing"

	"chatgpt2api/internal/storage"
)

// newTestUserProfileService spins up a UserProfileService backed by a JSON store inside a
// temp directory. We avoid touching disk paths between tests by relying on t.TempDir().
func newTestUserProfileService(t *testing.T) *UserProfileService {
	t.Helper()
	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	return NewUserProfileService(dir, backend)
}

// TestEnsureRegisteredUserOutcomeRewardsInviter confirms that when an invitee registers
// with an invite code, the outcome surfaces both the invitee and the inviter rewards so
// the HTTP layer can push notifications to both sides.
func TestEnsureRegisteredUserOutcomeRewardsInviter(t *testing.T) {
	svc := newTestUserProfileService(t)
	const (
		inviterID = "inviter-1"
		inviteeID = "invitee-1"
		freeQuota = 10
		invReward = 20
		invBonus  = 30
	)

	// Inviter registers first without an invite code.
	inviterOutcome, err := svc.EnsureRegisteredUserOutcome(inviterID, "", freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if inviterOutcome.FreeQuotaGranted != freeQuota {
		t.Fatalf("inviter free quota = %d, want %d", inviterOutcome.FreeQuotaGranted, freeQuota)
	}
	inviteCode := inviterOutcome.Profile.InviteCode
	if inviteCode == "" {
		t.Fatal("inviter profile is missing invite code")
	}

	// Invitee uses the inviter's code.
	inviteeOutcome, err := svc.EnsureRegisteredUserOutcome(inviteeID, inviteCode, freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("invitee registration failed: %v", err)
	}
	if inviteeOutcome.InviterID != inviterID {
		t.Fatalf("inviteeOutcome.InviterID = %q, want %q", inviteeOutcome.InviterID, inviterID)
	}
	if inviteeOutcome.InviteRewardGranted != invReward {
		t.Fatalf("invite reward granted = %d, want %d", inviteeOutcome.InviteRewardGranted, invReward)
	}
	if inviteeOutcome.InviteeBonusGranted != invBonus {
		t.Fatalf("invitee bonus = %d, want %d", inviteeOutcome.InviteeBonusGranted, invBonus)
	}
	expectedInviterTotal := freeQuota + invReward
	if inviteeOutcome.InviterQuotaAfter != expectedInviterTotal {
		t.Fatalf("inviter quota after = %d, want %d", inviteeOutcome.InviterQuotaAfter, expectedInviterTotal)
	}

	// Re-fetching the inviter profile shows the bumped quota – this is what /auth/session
	// will return so the bell can refresh the cached session quota in the browser.
	inviterRefreshed, err := svc.EnsureRegisteredUserOutcome(inviterID, "", freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("inviter refresh failed: %v", err)
	}
	if inviterRefreshed.FreeQuotaGranted != 0 {
		t.Fatalf("re-running EnsureRegisteredUser must be idempotent; granted %d", inviterRefreshed.FreeQuotaGranted)
	}
	if got := inviterRefreshed.Profile.ImageQuotaTotal; got == nil || *got != expectedInviterTotal {
		t.Fatalf("inviter total quota after invite = %v, want %d", got, expectedInviterTotal)
	}
	if inviterRefreshed.Profile.InviteRewardTotal != invReward {
		t.Fatalf("inviter cumulative reward total = %d, want %d", inviterRefreshed.Profile.InviteRewardTotal, invReward)
	}
}

// TestEnsureRegisteredUserOutcomeInviteIsIdempotent makes sure subsequent logins from the
// same invitee do not credit the inviter twice (a regression we already saw once).
func TestEnsureRegisteredUserOutcomeInviteIsIdempotent(t *testing.T) {
	svc := newTestUserProfileService(t)
	const (
		inviterID = "inviter-2"
		inviteeID = "invitee-2"
		freeQuota = 10
		invReward = 20
		invBonus  = 30
	)
	inviter, err := svc.EnsureRegisteredUserOutcome(inviterID, "", freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("inviter registration failed: %v", err)
	}
	first, err := svc.EnsureRegisteredUserOutcome(inviteeID, inviter.Profile.InviteCode, freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("first invitee registration failed: %v", err)
	}
	if first.InviteRewardGranted != invReward {
		t.Fatalf("first invitee call should grant the inviter reward, got %d", first.InviteRewardGranted)
	}
	second, err := svc.EnsureRegisteredUserOutcome(inviteeID, inviter.Profile.InviteCode, freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("second invitee registration failed: %v", err)
	}
	if second.InviteRewardGranted != 0 || second.InviteeBonusGranted != 0 || second.InviterID != "" {
		t.Fatalf("repeated invitee call must be a no-op, got outcome: %+v", second)
	}
}

func TestInviteSummaryCountsAllInvitedUsers(t *testing.T) {
	svc := newTestUserProfileService(t)
	const (
		inviterID = "inviter-summary"
		freeQuota = 10
		invReward = 20
		invBonus  = 30
	)
	inviter, err := svc.EnsureRegisteredUserOutcome(inviterID, "", freeQuota, invReward, invBonus)
	if err != nil {
		t.Fatalf("inviter registration failed: %v", err)
	}
	for i := 0; i < 21; i++ {
		if _, err := svc.EnsureRegisteredUserOutcome(fmt.Sprintf("invitee-summary-%02d", i), inviter.Profile.InviteCode, freeQuota, invReward, invBonus); err != nil {
			t.Fatalf("invitee registration failed: %v", err)
		}
	}
	summary := svc.InviteSummary(Identity{ID: inviterID, OwnerID: inviterID}, "https://example.test", invReward, invBonus)
	if got := summary["invited_count"]; got != 21 {
		t.Fatalf("invited_count = %v, want 21", got)
	}
	if got := len(summary["invited_users"].([]map[string]any)); got != 20 {
		t.Fatalf("invited_users len = %d, want 20", got)
	}
}
