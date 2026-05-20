package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

// handleNotifications serves the per-user notifications API. GET returns the most recent
// items (default 100) and POST without an id marks every notification as read.
func (a *App) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if a.notifications == nil {
		http.NotFound(w, r)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	owner := identityScope(identity)
	// Non-user identities (admin / API tokens / system) have no personal notification
	// inbox today. Instead of poisoning the dashboard with 403s every poll, respond
	// with an empty payload for read-style requests and 405 for mutations they cannot
	// perform.
	if identity.Role != service.AuthRoleUser || owner == "" {
		if r.URL.Path == "/api/notifications" && r.Method == http.MethodGet {
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}, "unread": 0})
			return
		}
		if r.URL.Path == "/api/notifications" && r.Method == http.MethodPost {
			util.WriteJSON(w, http.StatusOK, map[string]any{"updated": 0, "unread": 0})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := splitPath(r.URL.Path)
	if r.URL.Path == "/api/notifications" {
		switch r.Method {
		case http.MethodGet:
			limit := parsePositiveInt(r.URL.Query().Get("limit"), 100, 200)
			util.WriteJSON(w, http.StatusOK, map[string]any{
				"items":  a.notifications.ListForOwner(owner, limit),
				"unread": a.notifications.UnreadCountForOwner(owner),
			})
		case http.MethodPost:
			count := a.notifications.MarkAllRead(owner)
			util.WriteJSON(w, http.StatusOK, map[string]any{"updated": count, "unread": a.notifications.UnreadCountForOwner(owner)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "notifications" && parts[3] == "read" {
		notificationID := parts[2]
		switch r.Method {
		case http.MethodPost:
			item := a.notifications.MarkRead(owner, notificationID)
			if item == nil {
				util.WriteError(w, http.StatusNotFound, "notification not found")
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "unread": a.notifications.UnreadCountForOwner(owner)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "notifications" {
		notificationID := parts[2]
		switch r.Method {
		case http.MethodDelete:
			if !a.notifications.Delete(owner, notificationID) {
				util.WriteError(w, http.StatusNotFound, "notification not found")
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "unread": a.notifications.UnreadCountForOwner(owner)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	http.NotFound(w, r)
}

// handleAdminNotifications exposes admin-only operations such as broadcast and registration
// bonus backfill. POST /api/admin/notifications/broadcast accepts {title, body, category}.
// POST /api/admin/notifications/backfill triggers BackfillRegistrationBonus and emits a
// notification to every user that received a top-up.
func (a *App) handleAdminNotifications(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "permission denied")
		return
	}
	if a.notifications == nil || a.profiles == nil {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Path {
	case "/api/admin/notifications/broadcast":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		title := util.Clean(body["title"])
		text := util.Clean(body["body"])
		if title == "" && text == "" {
			util.WriteError(w, http.StatusBadRequest, "title or body is required")
			return
		}
		category := util.Clean(body["category"])
		if category == "" {
			category = service.NotificationCategorySystem
		}
		ownerIDs := a.profiles.ListAllOwnerIDs()
		count := a.notifications.Broadcast(ownerIDs, category, title, text, util.StringMap(body["meta"]))
		util.WriteJSON(w, http.StatusOK, map[string]any{"delivered": count, "recipients": len(ownerIDs)})
	case "/api/admin/notifications/backfill":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		freeQuota := a.config.UserFreeQuota()
		grants, err := a.profiles.BackfillRegistrationBonus(freeQuota)
		if err != nil {
			util.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		credited := 0
		for _, grant := range grants {
			if grant.Amount <= 0 {
				continue
			}
			credited++
			a.notifications.Push(grant.OwnerID, service.NotificationCategoryRegistrationBackfill,
				"注册赠送已补发",
				fmt.Sprintf("已为你的账号补发注册赠送 %d 次出图额度，当前总额度 %d。感谢你的使用。", grant.Amount, grant.QuotaAfter),
				map[string]any{
					"amount":      grant.Amount,
					"quota_after": grant.QuotaAfter,
					"granted_at":  grant.GrantedAt,
				})
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"scanned":    len(grants),
			"credited":   credited,
			"free_quota": freeQuota,
		})
	default:
		http.NotFound(w, r)
	}
}

// emitRegistrationNotifications turns a RegistrationOutcome into per-user notifications.
// It is safe to call with a zero outcome (nothing happens).
func (a *App) emitRegistrationNotifications(ownerID string, outcome service.RegistrationOutcome) {
	if a == nil || a.notifications == nil {
		return
	}
	if outcome.FreeQuotaGranted > 0 {
		quotaAfter := 0
		if outcome.Profile.ImageQuotaTotal != nil {
			quotaAfter = *outcome.Profile.ImageQuotaTotal
		}
		a.notifications.Push(ownerID, service.NotificationCategoryRegistrationBonus,
			"欢迎加入，注册赠送已发放",
			fmt.Sprintf("已为你发放 %d 次出图额度，立即开始创作吧。", outcome.FreeQuotaGranted),
			map[string]any{"amount": outcome.FreeQuotaGranted, "quota_after": quotaAfter})
	}
	if outcome.InviteeBonusGranted > 0 {
		a.notifications.Push(ownerID, service.NotificationCategoryInviteeBonus,
			"邀请奖励已到账",
			fmt.Sprintf("通过邀请加入，额外获得 %d 次出图额度。", outcome.InviteeBonusGranted),
			map[string]any{"amount": outcome.InviteeBonusGranted, "inviter_id": outcome.InviterID})
	}
	if outcome.InviterID != "" && outcome.InviteRewardGranted > 0 {
		a.notifications.Push(outcome.InviterID, service.NotificationCategoryInviteReward,
			"邀请的好友完成注册",
			fmt.Sprintf("感谢分享！邀请好友成功，获得 %d 次出图额度奖励。", outcome.InviteRewardGranted),
			map[string]any{"amount": outcome.InviteRewardGranted, "invitee_id": ownerID, "quota_after": outcome.InviterQuotaAfter})
	}
}

// emitCheckinNotification is invoked from handleCheckin when the user actually earns a
// reward (>0). Days with zero rewards do not produce a notification.
func (a *App) emitCheckinNotification(ownerID string, day, reward int, quotaAfter int) {
	if a == nil || a.notifications == nil || reward <= 0 {
		return
	}
	a.notifications.Push(ownerID, service.NotificationCategoryCheckinReward,
		"今日签到成功",
		fmt.Sprintf("第 %d 天签到，获得 %d 次出图额度，当前总额度 %d。", day, reward, quotaAfter),
		map[string]any{"reward": reward, "day": day, "quota_after": quotaAfter})
}

func (a *App) emitAdminQuotaNotification(ownerID string, delta, quotaAfter int) map[string]any {
	if a == nil || a.notifications == nil || delta == 0 {
		return nil
	}
	title := "点数已到账"
	text := fmt.Sprintf("管理员已为你增加 %d 点，当前总点数 %d。", delta, quotaAfter)
	if delta < 0 {
		title = "点数已调整"
		text = fmt.Sprintf("管理员已调整你的点数 %d 点，当前总点数 %d。", delta, quotaAfter)
	}
	return a.notifications.Push(ownerID, service.NotificationCategoryAdminQuotaAdjustment,
		title,
		text,
		map[string]any{"amount": delta, "delta": delta, "quota_after": quotaAfter})
}

func parsePositiveInt(value string, defaultValue, max int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue
	}
	n := util.ToInt(value, defaultValue)
	if n <= 0 {
		return defaultValue
	}
	if max > 0 && n > max {
		return max
	}
	return n
}
