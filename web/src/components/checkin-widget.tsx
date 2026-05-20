"use client";

import { useEffect, useMemo, useState } from "react";
import { CalendarCheck2, Gift, LoaderCircle, Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  fetchCheckinStatus,
  submitCheckin,
  type CheckinState,
} from "@/lib/api";
import {
  getCachedAuthSession,
  setVerifiedAuthSession,
} from "@/lib/session";
import { cn } from "@/lib/utils";
import { hasAPIPermission, type StoredAuthSession } from "@/store/auth";

const quotaRefreshEvent = "chatgpt2api:quota-refresh";
const checkinSyncEvent = "chatgpt2api:checkin-sync";
const defaultCheckinRewards = [1, 1, 2, 2, 3, 3, 7];

type CheckinSyncDetail = { state: CheckinState };

function broadcastCheckinState(next: CheckinState | null) {
  if (typeof window === "undefined" || !next) return;
  window.dispatchEvent(new CustomEvent<CheckinSyncDetail>(checkinSyncEvent, { detail: { state: next } }));
}

function normalizeState(value: CheckinState | null): CheckinState | null {
  if (!value || !value.enabled) {
    return value;
  }
  const rewards = Array.isArray(value.rewards) && value.rewards.length > 0
    ? value.rewards.map((reward) => Math.max(0, Number(reward || 0) || 0))
    : defaultCheckinRewards;
  const safeRewards = rewards.some((reward) => reward > 0) ? rewards : defaultCheckinRewards;
  const nextDay = Math.max(1, Number(value.next_day || 1) || 1);
  return {
    ...value,
    rewards: safeRewards,
    next_day: nextDay,
    next_reward: Math.max(0, Number(value.next_reward || safeRewards[nextDay - 1] || 0) || 0),
    checked_today: Boolean(value.checked_today || value.already_checked_in_today),
  };
}

function autoPopupKey(session: StoredAuthSession, state: CheckinState) {
  return `chatgpt2api:checkin:auto:${session.subjectId}:${state.today || "today"}`;
}

function addOptionalNumber(value: number | null | undefined, delta: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return delta > 0 ? delta : value ?? null;
  }
  return Math.max(0, value + delta);
}

async function syncQuotaFromState(state: CheckinState, reward = 0) {
  const session = getCachedAuthSession();
  if (!session) {
    return;
  }
  const safeReward = Math.max(0, Number(reward || 0) || 0);
  const nextTotal = state.image_quota_total ?? addOptionalNumber(session.imageQuotaTotal, safeReward);
  const nextRemaining = state.image_quota_remaining ?? addOptionalNumber(session.imageQuotaRemaining, safeReward);
  await setVerifiedAuthSession({
    ...session,
    imageQuotaTotal: nextTotal,
    imageQuotaUsed: Math.max(0, Number(state.image_quota_used ?? session.imageQuotaUsed ?? 0) || 0),
    imageQuotaRemaining: nextRemaining,
  });
  window.dispatchEvent(new Event(quotaRefreshEvent));
}

export function CheckinWidget({
  session,
  className,
}: {
  session: StoredAuthSession | null | undefined;
  className?: string;
}) {
  const [state, setState] = useState<CheckinState | null>(null);
  const [open, setOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const canUseCheckin = hasAPIPermission(session, "GET", "/api/checkin/status");
  const normalizedState = useMemo(() => normalizeState(state), [state]);
  const isEnabled = Boolean(normalizedState?.enabled);
  const checkedToday = Boolean(normalizedState?.checked_today);
  const nextReward = Math.max(0, Number(normalizedState?.next_reward || 0) || 0);

  useEffect(() => {
    if (!canUseCheckin) {
      setState(null);
      return;
    }
    let active = true;
    const load = async () => {
      setIsLoading(true);
      try {
        const data = await fetchCheckinStatus();
        if (active) {
          setState(data);
          broadcastCheckinState(data);
        }
      } catch {
        if (active) {
          setState(null);
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [canUseCheckin]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const handler = (event: Event) => {
      const detail = (event as CustomEvent<CheckinSyncDetail>).detail;
      if (!detail || !detail.state) return;
      setState(detail.state);
    };
    window.addEventListener(checkinSyncEvent, handler as EventListener);
    return () => window.removeEventListener(checkinSyncEvent, handler as EventListener);
  }, []);

  useEffect(() => {
    if (!session || !normalizedState?.enabled || normalizedState.checked_today) {
      return;
    }
    const key = autoPopupKey(session, normalizedState);
    if (window.localStorage.getItem(key) === "1") {
      return;
    }
    const timer = window.setTimeout(() => {
      window.localStorage.setItem(key, "1");
      setOpen(true);
    }, 900);
    return () => window.clearTimeout(timer);
  }, [normalizedState, session]);

  if (!canUseCheckin || !isEnabled) {
    return null;
  }

  const handleCheckin = async () => {
    if (checkedToday) {
      // Defensive: another widget instance (or another tab) already checked in today.
      // Don't fire a second POST that would just produce already_checked=true noise.
      return;
    }
    setIsSubmitting(true);
    try {
      const data = await submitCheckin();
      const nextState = data.state;
      setState(nextState);
      broadcastCheckinState(nextState);
      await syncQuotaFromState(nextState, data.already_checked ? 0 : data.reward);
      toast.success(data.already_checked ? "今日已经签到" : `签到成功，获得 ${data.reward} 点`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "签到失败");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <>
      <Button
        type="button"
        variant="outline"
        className={cn(
          "h-9 rounded-full border-[#bfdbfe] bg-[#edf4ff] px-3 text-[#1456f0] shadow-none hover:bg-[#dbeafe] dark:border-sky-800/80 dark:bg-sky-950/30 dark:text-sky-300 dark:hover:bg-sky-950/50",
          className,
        )}
        onClick={() => setOpen(true)}
        title={checkedToday ? "今日已签到" : `签到领 ${nextReward} 点`}
      >
        {isLoading ? (
          <LoaderCircle className="size-4 animate-spin" />
        ) : checkedToday ? (
          <CalendarCheck2 className="size-4" />
        ) : (
          <Gift className="size-4" />
        )}
        <span className="hidden text-xs font-semibold sm:inline">
          {checkedToday ? "已签到" : `签到 +${nextReward}`}
        </span>
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="overflow-hidden rounded-[28px] border-0 bg-white p-0 shadow-[0_34px_100px_rgba(15,23,42,0.24)] dark:bg-slate-950">
          <div className="bg-[radial-gradient(circle_at_20%_10%,rgba(20,86,240,0.18),transparent_34%),linear-gradient(135deg,#ffffff_0%,#f6f9ff_55%,#fff3fb_100%)] px-6 py-6 dark:bg-[radial-gradient(circle_at_20%_10%,rgba(56,189,248,0.18),transparent_34%),linear-gradient(135deg,#020617_0%,#0f172a_60%,#1e1b4b_100%)]">
            <DialogHeader className="gap-3">
              <div className="flex size-12 items-center justify-center rounded-2xl bg-[#1456f0] text-white shadow-[0_12px_24px_rgba(20,86,240,0.24)]">
                <Sparkles className="size-5" />
              </div>
              <div>
                <DialogTitle className="text-2xl tracking-[-0.03em] text-[#18181b] dark:text-white">
                  每日签到领点数
                </DialogTitle>
                <DialogDescription className="mt-2 leading-6 text-[#45515e] dark:text-white/68">
                  连续签到可获得更多出图点数，点数会实时同步到账号。
                </DialogDescription>
              </div>
            </DialogHeader>
          </div>

          <div className="px-6 pb-6 pt-5">
            <div className="grid grid-cols-7 gap-2">
              {(normalizedState?.rewards || []).slice(0, 7).map((reward, index) => {
                const day = index + 1;
                const active = !checkedToday && day === normalizedState?.next_day;
                const completed = checkedToday && day <= Math.max(0, Number(normalizedState?.checkin_streak || 0) || 0);
                return (
                  <div
                    key={day}
                    className={cn(
                      "rounded-2xl border px-1.5 py-3 text-center",
                      active
                        ? "border-[#1456f0] bg-[#edf4ff] text-[#1456f0] shadow-[0_10px_24px_rgba(20,86,240,0.14)]"
                        : "border-slate-200 bg-slate-50 text-slate-700 dark:border-white/10 dark:bg-white/5 dark:text-white/70",
                      completed ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/30 dark:text-emerald-300" : "",
                    )}
                  >
                    <div className="text-[11px] font-medium">第 {day} 天</div>
                    <div className="mt-1 text-base font-black">+{reward}</div>
                  </div>
                );
              })}
            </div>
            <div className="mt-4 rounded-2xl bg-slate-50 px-4 py-3 text-sm text-slate-600 dark:bg-white/5 dark:text-white/66">
              累计签到 {normalizedState?.checkin_total || 0} 天，累计领取 {normalizedState?.checkin_reward_total || 0} 点。
            </div>
          </div>

          <DialogFooter className="px-6 pb-6">
            <Button type="button" variant="secondary" size="lg" onClick={() => setOpen(false)}>
              稍后再说
            </Button>
            <Button
              type="button"
              size="lg"
              className="bg-[#181e25] text-white hover:bg-[#10151c]"
              onClick={() => void handleCheckin()}
              disabled={checkedToday || isSubmitting}
            >
              {isSubmitting ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <Gift data-icon="inline-start" />}
              {checkedToday ? "今日已签到" : `签到领取 ${nextReward} 点`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
