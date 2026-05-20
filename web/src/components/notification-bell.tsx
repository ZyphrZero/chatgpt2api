"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { BellRing, Check, Coins, Gift, ImagePlus, ListChecks, LoaderCircle, MailOpen, Megaphone, Sparkles } from "lucide-react";
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
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AnnouncementMarkdown } from "@/components/announcement-markdown";
import {
  fetchNotifications,
  fetchVisibleAnnouncements,
  markAllNotificationsRead,
  markNotificationRead,
  verifySession,
  type Announcement,
  type AnnouncementTarget,
  type AppNotification,
} from "@/lib/api";
import { authSessionFromLoginResponse, getCachedAuthSession, setVerifiedAuthSession } from "@/lib/session";
import { cn } from "@/lib/utils";

const POLL_INTERVAL_MS = 60_000;
const QUOTA_AFFECTING_CATEGORIES = new Set([
  "registration_bonus",
  "registration_bonus_backfill",
  "invite_reward",
  "invitee_bonus",
  "checkin_reward",
  "admin_quota_adjustment",
]);
const INVITE_AFFECTING_CATEGORIES = new Set(["invite_reward", "invitee_bonus"]);
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";
export const INVITE_REFRESH_EVENT = "chatgpt2api:invite-refresh";

function announcementBatchKey(target: AnnouncementTarget | undefined, announcements: Announcement[]) {
  if (!target || announcements.length === 0) {
    return "";
  }
  return `chatgpt2api:announcement-dialog:${target}:${announcements.map((item) => item.id).sort().join(",")}`;
}

function hasSeenAnnouncementBatch(key: string) {
  if (!key || typeof window === "undefined") {
    return true;
  }
  try {
    return window.sessionStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}

function markAnnouncementBatchSeen(key: string) {
  if (!key || typeof window === "undefined") {
    return;
  }
  try {
    window.sessionStorage.setItem(key, "1");
  } catch {
    // If sessionStorage is blocked, fall back to normal dialog behavior for this page view.
  }
}

function isCompactNotificationViewport() {
  if (typeof window === "undefined" || !window.matchMedia) {
    return false;
  }
  return window.matchMedia("(max-width: 640px), (pointer: coarse)").matches;
}

// Pulls a fresh session from the backend so quota-affecting notifications (invite reward,
// invitee bonus, registration backfill, ...) update the header counter without a hard
// reload. Failures are swallowed: the bell will retry on the next poll.
async function refreshSessionQuota() {
  const session = getCachedAuthSession();
  if (!session?.key) {
    return;
  }
  try {
    const data = await verifySession(session.key);
    const next = authSessionFromLoginResponse(data, session.key);
    await setVerifiedAuthSession(next);
    if (typeof window !== "undefined") {
      window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
    }
  } catch {
    // ignore – next poll will retry
  }
}

function categoryStyles(category: string) {
  switch (category) {
    case "registration_bonus":
    case "registration_bonus_backfill":
      return { icon: Gift, accent: "text-emerald-600", bg: "bg-emerald-50", ring: "ring-emerald-200" };
    case "invite_reward":
      return { icon: Coins, accent: "text-amber-600", bg: "bg-amber-50", ring: "ring-amber-200" };
    case "invitee_bonus":
      return { icon: Sparkles, accent: "text-fuchsia-600", bg: "bg-fuchsia-50", ring: "ring-fuchsia-200" };
    case "checkin_reward":
      return { icon: ListChecks, accent: "text-sky-600", bg: "bg-sky-50", ring: "ring-sky-200" };
    case "admin_quota_adjustment":
      return { icon: Coins, accent: "text-blue-600", bg: "bg-blue-50", ring: "ring-blue-200" };
    case "share_visit":
      return { icon: ImagePlus, accent: "text-indigo-600", bg: "bg-indigo-50", ring: "ring-indigo-200" };
    case "system":
    default:
      return { icon: Megaphone, accent: "text-slate-600", bg: "bg-slate-50", ring: "ring-slate-200" };
  }
}

function formatRelative(value: string | undefined) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const diffSeconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (diffSeconds < 60) return "刚刚";
  const minutes = Math.round(diffSeconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.round(hours / 24);
  if (days < 14) return `${days} 天前`;
  return date.toLocaleDateString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" });
}

export function NotificationBell({
  className,
  initialUnread = 0,
  announcementTarget,
  autoAnnouncementDialog = false,
}: {
  className?: string;
  initialUnread?: number;
  announcementTarget?: AnnouncementTarget;
  autoAnnouncementDialog?: boolean;
}) {
  const [items, setItems] = useState<AppNotification[]>([]);
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [unread, setUnread] = useState(initialUnread);
  const [open, setOpen] = useState(false);
  const [announcementDialogOpen, setAnnouncementDialogOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [hasFetched, setHasFetched] = useState(false);

  useEffect(() => {
    if (!announcementTarget) {
      setAnnouncements([]);
      return;
    }
    let active = true;
    const loadAnnouncements = async () => {
      try {
        const data = await fetchVisibleAnnouncements(announcementTarget);
        if (active) {
          setAnnouncements(data.items);
        }
      } catch {
        if (active) {
          setAnnouncements([]);
        }
      }
    };
    void loadAnnouncements();
    return () => {
      active = false;
    };
  }, [announcementTarget]);

  useEffect(() => {
    if (!autoAnnouncementDialog || announcements.length === 0) {
      return;
    }
    const batchKey = announcementBatchKey(announcementTarget, announcements);
    if (hasSeenAnnouncementBatch(batchKey)) {
      return;
    }
    const timer = window.setTimeout(() => {
      markAnnouncementBatchSeen(batchKey);
      setAnnouncementDialogOpen(true);
    }, isCompactNotificationViewport() ? 1800 : 700);
    return () => window.clearTimeout(timer);
  }, [announcements, announcementTarget, autoAnnouncementDialog]);

  const refresh = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) {
      setIsLoading(true);
    }
    try {
      const data = await fetchNotifications(50);
      setItems((prev) => {
        const previousIds = new Set(prev.map((item) => item.id));
        const incoming = data.items.filter((item) => !previousIds.has(item.id));
        const firstUnreadItems = prev.length === 0 ? data.items.filter((item) => !item.read_at) : [];
        const changedItems = [...incoming, ...firstUnreadItems];
        if (changedItems.some((item) => QUOTA_AFFECTING_CATEGORIES.has(item.category))) {
          void refreshSessionQuota();
        }
        if (
          typeof window !== "undefined" &&
          changedItems.some((item) => INVITE_AFFECTING_CATEGORIES.has(item.category))
        ) {
          window.dispatchEvent(new Event(INVITE_REFRESH_EVENT));
        }
        return data.items;
      });
      setUnread(data.unread);
      setHasFetched(true);
    } catch (error) {
      if (!opts.silent) {
        toast.error(error instanceof Error ? error.message : "无法加载通知");
      }
    } finally {
      if (!opts.silent) {
        setIsLoading(false);
      }
    }
  }, []);

  // Poll quietly only while the page is visible; this keeps the badge useful without
  // creating background request noise during long creative sessions.
  useEffect(() => {
    const refreshIfVisible = () => {
      if (document.hidden) {
        return;
      }
      void refresh({ silent: true });
    };
    const handleVisibilityChange = () => {
      if (!document.hidden) {
        void refresh({ silent: true });
      }
    };

    refreshIfVisible();
    const interval = window.setInterval(() => {
      refreshIfVisible();
    }, POLL_INTERVAL_MS);
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [refresh]);

  // When the dropdown opens we re-pull so items reflect any optimistic changes.
  useEffect(() => {
    if (open && !hasFetched) {
      void refresh();
    }
  }, [hasFetched, open, refresh]);

  const handleMarkOne = useCallback(
    async (notification: AppNotification) => {
      if (notification.read_at) {
        return;
      }
      try {
        const result = await markNotificationRead(notification.id);
        setUnread(result.unread);
        setItems((prev) => prev.map((item) => (item.id === notification.id ? result.item : item)));
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "标记已读失败");
      }
    },
    [],
  );

  const handleMarkAll = useCallback(async () => {
    if (unread === 0) {
      return;
    }
    try {
      const result = await markAllNotificationsRead();
      setUnread(result.unread);
      setItems((prev) => prev.map((item) => ({ ...item, read_at: item.read_at || new Date().toISOString() })));
      toast.success("已将全部通知标记为已读");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "操作失败");
    }
  }, [unread]);

  const badge = useMemo(() => {
    const count = unread + announcements.length;
    if (count <= 0) return null;
    if (count > 99) return "99+";
    return String(count);
  }, [announcements.length, unread]);

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label="通知中心"
            className={cn("relative rounded-full border-border/60 bg-background/80 shadow-sm backdrop-blur", className)}
          >
            <BellRing className="size-4" />
            {badge ? (
              <span className="absolute -right-0.5 -top-0.5 inline-flex min-w-[18px] items-center justify-center rounded-full bg-rose-500 px-1 text-[10px] font-bold leading-[18px] text-white shadow ring-2 ring-background">
                {badge}
              </span>
            ) : null}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="end"
          className="w-[min(calc(100vw-1rem),380px)] rounded-[22px] border-border/60 p-0 shadow-xl sm:rounded-3xl"
        >
          <div className="flex items-center justify-between border-b border-border/60 px-3.5 py-2.5 sm:px-4 sm:py-3">
            <div className="flex items-center gap-2">
              <BellRing className="size-4 text-[#1456f0]" />
              <span className="font-semibold tracking-tight">通知中心</span>
              {unread > 0 ? (
                <span className="rounded-full bg-rose-500/15 px-2 py-0.5 text-[11px] font-medium text-rose-600">
                  {unread} 条未读
                </span>
              ) : null}
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={unread === 0}
              onClick={() => void handleMarkAll()}
              className="h-7 gap-1 px-2 text-xs sm:h-8"
            >
              <MailOpen className="size-3.5" />
              全部已读
            </Button>
          </div>
          <div className="max-h-[min(56vh,420px)] overflow-y-auto sm:max-h-[60vh]">
            {announcements.length > 0 ? (
              <div className="border-b border-border/60 p-2.5 sm:p-3">
                <div className="mb-2 flex items-center gap-2 px-1 text-xs font-semibold text-muted-foreground">
                  <Megaphone className="size-3.5 text-amber-600" />
                  网站公告
                </div>
                <div className="flex flex-col gap-2">
                  {announcements.map((announcement) => (
                    <aside
                      key={announcement.id}
                      className={cn(
                        "rounded-[16px] border border-amber-200/80 bg-amber-50/90 px-2.5 py-2 text-left sm:rounded-2xl sm:px-3",
                      )}
                    >
                      <div className="flex items-center gap-2">
                        <Megaphone className="size-3.5 shrink-0 text-amber-700" />
                        <p className="truncate text-sm font-semibold text-stone-900">{announcement.title.trim() || "公告"}</p>
                      </div>
                      <AnnouncementMarkdown className="mt-1 line-clamp-3 text-stone-700 sm:line-clamp-4">
                        {announcement.content}
                      </AnnouncementMarkdown>
                    </aside>
                  ))}
                </div>
              </div>
            ) : null}
            {isLoading && items.length === 0 ? (
              <div className="flex items-center justify-center px-4 py-7 text-sm text-muted-foreground sm:py-10">
                <LoaderCircle className="mr-2 size-4 animate-spin" />
                正在加载通知…
              </div>
            ) : items.length === 0 ? (
              <div className="px-4 py-7 text-center text-sm text-muted-foreground sm:py-10">暂无站内通知</div>
            ) : (
              <ul className="divide-y divide-border/60">
                {items.map((item) => {
                  const styles = categoryStyles(item.category);
                  const Icon = styles.icon;
                  const isUnread = !item.read_at;
                  return (
                    <li
                      key={item.id}
                      className={cn(
                        "flex cursor-pointer items-start gap-2.5 px-3 py-2.5 transition-colors hover:bg-muted/50 sm:gap-3 sm:px-4 sm:py-3",
                        isUnread ? "bg-blue-50/40" : "",
                      )}
                      onClick={() => void handleMarkOne(item)}
                    >
                      <span
                        className={cn(
                          "mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-[16px] ring-1 sm:size-9 sm:rounded-2xl",
                          styles.bg,
                          styles.ring,
                        )}
                      >
                        <Icon className={cn("size-4", styles.accent)} />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className={cn("truncate text-sm font-semibold", isUnread ? "text-foreground" : "text-muted-foreground")}>
                            {item.title || "通知"}
                          </span>
                          {isUnread ? <span className="size-1.5 rounded-full bg-rose-500" /> : null}
                        </div>
                        {item.body ? (
                          <p className="mt-0.5 line-clamp-2 whitespace-pre-line text-xs leading-5 text-muted-foreground sm:line-clamp-3">
                            {item.body}
                          </p>
                        ) : null}
                        <div className="mt-1 text-[11px] text-muted-foreground">{formatRelative(item.created_at)}</div>
                      </div>
                      {!isUnread ? <Check className="mt-1 size-3.5 text-emerald-500" /> : null}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </PopoverContent>
      </Popover>

      <Dialog open={announcementDialogOpen} onOpenChange={setAnnouncementDialogOpen}>
        <DialogContent className="max-h-[min(82vh,620px)] overflow-hidden p-0">
          <div className="border-b border-amber-100 bg-gradient-to-br from-amber-50 via-white to-sky-50 px-4 py-4 sm:px-6 sm:py-5">
            <DialogHeader>
              <div className="flex size-10 items-center justify-center rounded-2xl bg-amber-500 text-white shadow-[0_12px_28px_rgba(245,158,11,0.24)] sm:size-11">
                <Megaphone className="size-5" />
              </div>
              <DialogTitle className="text-xl tracking-[-0.03em] text-stone-950 sm:text-2xl">网站公告</DialogTitle>
              <DialogDescription>请查看最新通知，关闭后仍可通过顶部铃铛再次打开。</DialogDescription>
            </DialogHeader>
          </div>
          <div className="max-h-[min(54vh,390px)] overflow-y-auto px-4 py-3 sm:max-h-[min(58vh,440px)] sm:px-5 sm:py-4">
            <div className="flex flex-col gap-3">
              {announcements.map((announcement) => (
                <aside
                  key={announcement.id}
                  className="rounded-[18px] border border-stone-200 bg-white px-3 py-2.5 shadow-sm sm:rounded-2xl sm:px-4 sm:py-3"
                >
                  <p className="text-base font-semibold text-stone-950">{announcement.title.trim() || "公告"}</p>
                  <AnnouncementMarkdown className="mt-2 text-stone-700">{announcement.content}</AnnouncementMarkdown>
                </aside>
              ))}
            </div>
          </div>
          <DialogFooter className="border-t border-stone-100 bg-stone-50 px-4 py-3 sm:px-5 sm:py-4">
            <Button type="button" onClick={() => setAnnouncementDialogOpen(false)} className="rounded-full px-6">
              我知道了
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export default NotificationBell;
