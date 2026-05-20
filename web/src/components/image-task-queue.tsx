"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { CheckCircle2, Clock3, ClipboardList, LoaderCircle, Sparkles } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import {
  ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY,
  IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT,
  IMAGE_CONVERSATIONS_CHANGED_EVENT,
  listImageConversations,
  type ImageConversation,
  type ImageConversationMode,
  type ImageTurn,
  type ImageTurnStatus,
} from "@/store/image-conversations";
import {
  getImageTurnProgressSnapshot,
  imageTurnProgressKey,
  subscribeImageTurnProgress,
} from "@/store/image-turn-progress";

type TaskQueueItem = {
  conversationId: string;
  conversationTitle: string;
  turn: ImageTurn;
  totalCount: number;
  completedCount: number;
  failedCount: number;
  cancelledCount: number;
  loadingCount: number;
};

type RecentQueueCompletion = TaskQueueItem & {
  key: string;
  completedAt: number;
  finalStatus: ImageTurnStatus;
};

function isTurnBusy(turn: ImageTurn) {
  return (
    turn.status === "queued" ||
    turn.status === "generating" ||
    turn.images.some((image) => image.status === "loading")
  );
}

function isTerminalTurnStatus(status: ImageTurnStatus) {
  return status === "success" || status === "message" || status === "error" || status === "cancelled";
}

function formatElapsedClock(totalSeconds: number) {
  const safeSeconds = Math.max(0, totalSeconds);
  const hours = Math.floor(safeSeconds / 3600);
  const minutes = Math.floor((safeSeconds % 3600) / 60);
  const seconds = safeSeconds % 60;
  if (hours > 0) {
    return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function formatQueueTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function getModeLabel(mode: ImageConversationMode) {
  if (mode === "chat") {
    return "对话";
  }
  if (mode === "edit") {
    return "继续编辑";
  }
  if (mode === "image") {
    return "参考图";
  }
  return "文生图";
}

function getStatusLabel(status: ImageTurnStatus) {
  if (status === "queued") {
    return "排队中";
  }
  if (status === "generating") {
    return "处理中";
  }
  if (status === "success") {
    return "已完成";
  }
  if (status === "message") {
    return "文本回复";
  }
  if (status === "cancelled") {
    return "已终止";
  }
  return "失败";
}

function getStatusClass(status: ImageTurnStatus) {
  if (status === "queued") {
    return "bg-amber-50 text-amber-700 ring-amber-100 dark:bg-amber-950/30 dark:text-amber-300 dark:ring-amber-800";
  }
  if (status === "generating") {
    return "bg-sky-50 text-[#1456f0] ring-sky-100 dark:bg-sky-950/30 dark:text-sky-300 dark:ring-sky-800";
  }
  return "bg-muted text-muted-foreground ring-border";
}

function getCompletionTone(status: ImageTurnStatus) {
  if (status === "success" || status === "message") {
    return {
      iconClass: "bg-emerald-50 text-emerald-700 ring-emerald-100 dark:bg-emerald-950/30 dark:text-emerald-300 dark:ring-emerald-800",
      badgeClass: "bg-emerald-50 text-emerald-700 ring-emerald-100 dark:bg-emerald-950/30 dark:text-emerald-300 dark:ring-emerald-800",
      barClass: "bg-emerald-500",
      message: "任务已完成",
    };
  }
  if (status === "cancelled") {
    return {
      iconClass: "bg-amber-50 text-amber-700 ring-amber-100 dark:bg-amber-950/30 dark:text-amber-300 dark:ring-amber-800",
      badgeClass: "bg-amber-50 text-amber-700 ring-amber-100 dark:bg-amber-950/30 dark:text-amber-300 dark:ring-amber-800",
      barClass: "bg-amber-500",
      message: "任务已终止",
    };
  }
  return {
    iconClass: "bg-rose-50 text-rose-700 ring-rose-100 dark:bg-rose-950/30 dark:text-rose-300 dark:ring-rose-800",
    badgeClass: "bg-rose-50 text-rose-700 ring-rose-100 dark:bg-rose-950/30 dark:text-rose-300 dark:ring-rose-800",
    barClass: "bg-rose-500",
    message: "任务失败",
  };
}

function getQueueItem(conversation: ImageConversation, turn: ImageTurn): TaskQueueItem {
  const completedCount = turn.images.filter((image) => image.status === "success" || image.status === "message").length;
  const failedCount = turn.images.filter((image) => image.status === "error").length;
  const cancelledCount = turn.images.filter((image) => image.status === "cancelled").length;
  const loadingCount = turn.images.filter((image) => image.status === "loading").length;
  const totalCount = Math.max(1, turn.mode === "chat" ? 1 : turn.count || turn.images.length || 1);
  return {
    conversationId: conversation.id,
    conversationTitle: conversation.title,
    turn,
    totalCount,
    completedCount,
    failedCount,
    cancelledCount,
    loadingCount,
  };
}

function getTaskQueueItems(conversations: ImageConversation[]) {
  const items = conversations.flatMap((conversation) =>
    conversation.turns.flatMap((turn) => {
      if (!isTurnBusy(turn)) {
        return [];
      }

      return [getQueueItem(conversation, turn)];
    }),
  );

  return items.sort((a, b) => a.turn.createdAt.localeCompare(b.turn.createdAt));
}

function scheduleQueueIdleRead(callback: () => void) {
  if (typeof window === "undefined") {
    return () => {};
  }
  if ("requestIdleCallback" in window) {
    const idleId = window.requestIdleCallback(callback, { timeout: 1200 });
    return () => window.cancelIdleCallback(idleId);
  }
  const timer = globalThis.setTimeout(callback, 300);
  return () => globalThis.clearTimeout(timer);
}

function findQueueItem(conversations: ImageConversation[], conversationId: string, turnId: string) {
  const conversation = conversations.find((item) => item.id === conversationId);
  const turn = conversation?.turns.find((item) => item.id === turnId);
  return conversation && turn ? getQueueItem(conversation, turn) : null;
}

function useImageConversationsForQueue() {
  const [conversations, setConversations] = useState<ImageConversation[]>([]);

  useEffect(() => {
    let active = true;
    let cancelScheduledRead: () => void = () => {};

    const loadConversations = async () => {
      try {
        const items = await listImageConversations();
        if (active) {
          setConversations(items);
        }
      } catch {
        if (active) {
          setConversations([]);
        }
      }
    };

    const scheduleRefresh = () => {
      cancelScheduledRead();
      cancelScheduledRead = scheduleQueueIdleRead(() => {
        void loadConversations();
      });
    };

    const handleConversationChanged = () => {
      cancelScheduledRead();
      void loadConversations();
    };

    scheduleRefresh();
    window.addEventListener("focus", scheduleRefresh);
    window.addEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationChanged);
    return () => {
      active = false;
      cancelScheduledRead();
      window.removeEventListener("focus", scheduleRefresh);
      window.removeEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationChanged);
    };
  }, []);

  return conversations;
}

function QueueItem({
  item,
  now,
  onOpenConversation,
}: {
  item: TaskQueueItem;
  now: number;
  onOpenConversation: (conversationId: string) => void;
}) {
  const progress = getImageTurnProgressSnapshot()[imageTurnProgressKey(item.conversationId, item.turn.id)];
  const settledCount = item.completedCount + item.failedCount + item.cancelledCount;
  const progressPercent =
    settledCount > 0
      ? Math.min(100, Math.round((settledCount / item.totalCount) * 100))
      : item.turn.status === "generating"
        ? 8
        : 0;
  const elapsed =
    progress && Number.isFinite(progress.startedAt)
      ? formatElapsedClock(Math.floor((now - progress.startedAt) / 1000))
      : "";
  const detailParts = [
    getModeLabel(item.turn.mode),
    item.turn.model,
    item.turn.size,
    item.turn.quality ? `Quality ${item.turn.quality}` : "",
  ].filter(Boolean);
  const progressMessage =
    progress?.message || (item.turn.status === "queued" ? "等待前序任务" : item.turn.mode === "chat" ? "等待对话回复" : "等待生成结果");
  const progressDetail =
    progress?.detail ||
    (item.turn.mode === "chat"
      ? "对话任务处理中"
      : item.loadingCount > 0
        ? `还有 ${item.loadingCount} 张图片处理中`
        : "");

  return (
    <button
      type="button"
      className="w-full rounded-2xl border border-[#f2f3f5] bg-white p-2.5 text-left shadow-[0_4px_6px_rgba(0,0,0,0.05)] transition hover:border-[#dbe7ff] hover:bg-[#f8fbff] dark:border-border dark:bg-card dark:hover:bg-accent/40 sm:p-3"
      onClick={() => onOpenConversation(item.conversationId)}
    >
      <div className="flex items-start gap-2.5 sm:gap-3">
        <span
          className={cn(
            "mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full ring-1 sm:size-9",
            item.turn.status === "queued"
              ? "bg-amber-50 text-amber-700 ring-amber-100"
              : "bg-sky-50 text-[#1456f0] ring-sky-100",
          )}
        >
          {item.turn.status === "queued" ? <Clock3 className="size-4" /> : <LoaderCircle className="size-4 animate-spin" />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <p className="truncate text-[13px] font-semibold text-[#222222] dark:text-foreground sm:text-sm">
              {item.conversationTitle || item.turn.prompt || "未命名任务"}
            </p>
            <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1", getStatusClass(item.turn.status))}>
              {getStatusLabel(item.turn.status)}
            </span>
          </div>
          <p className="mt-1 line-clamp-2 text-xs leading-5 text-[#45515e] dark:text-muted-foreground">
            {item.turn.prompt || "无提示词内容"}
          </p>

          <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px] text-[#45515e] dark:text-muted-foreground">
            {detailParts.map((part, index) => (
              <span key={`${part}-${index}`} className="rounded-full bg-[#f0f0f0] px-2 py-0.5 dark:bg-muted">
                {part}
              </span>
            ))}
            <span className="rounded-full bg-[#f0f0f0] px-2 py-0.5 font-mono tabular-nums dark:bg-muted">
              {item.completedCount + item.failedCount + item.cancelledCount}/{item.totalCount}
            </span>
          </div>

          <div className="mt-3">
            <div className="mb-1.5 flex items-center justify-between gap-2 text-[11px] text-[#45515e] dark:text-muted-foreground">
              <span className="truncate font-medium text-[#222222] dark:text-foreground">{progressMessage}</span>
              <span className="shrink-0 font-mono tabular-nums">{progressPercent}%</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-[#edf2f7] dark:bg-muted">
              <div className="h-full rounded-full bg-[#1456f0] transition-[width] duration-300" style={{ width: `${progressPercent}%` }} />
            </div>
            <div className="mt-1.5 flex items-center justify-between gap-2 text-[11px] text-[#8e8e93] dark:text-muted-foreground">
              <span className="truncate">{progressDetail || formatQueueTime(item.turn.createdAt)}</span>
              {elapsed ? <span className="shrink-0 font-mono tabular-nums">已等待 {elapsed}</span> : null}
            </div>
          </div>
        </div>
      </div>
    </button>
  );
}

function CompletionItem({
  item,
  onOpenConversation,
}: {
  item: RecentQueueCompletion;
  onOpenConversation: (conversationId: string) => void;
}) {
  const tone = getCompletionTone(item.finalStatus);
  const settledCount = item.completedCount + item.failedCount + item.cancelledCount;
  const resultText =
    item.finalStatus === "success" || item.finalStatus === "message"
      ? `${settledCount}/${item.totalCount} 已完成`
      : item.turn.error || getStatusLabel(item.finalStatus);

  return (
    <button
      type="button"
      className="animate-in fade-in slide-in-from-top-1 zoom-in-95 w-full rounded-2xl border border-emerald-100 bg-white p-2.5 text-left shadow-[0_12px_24px_-18px_rgba(16,185,129,0.55)] duration-300 hover:border-emerald-200 hover:bg-emerald-50/45 dark:border-emerald-900/50 dark:bg-card dark:hover:bg-emerald-950/20 sm:p-3"
      onClick={() => onOpenConversation(item.conversationId)}
    >
      <div className="flex items-start gap-2.5 sm:gap-3">
        <span className={cn("relative mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full ring-1 sm:size-9", tone.iconClass)}>
          <span className="absolute inset-0 rounded-full bg-current opacity-15 animate-ping" />
          <CheckCircle2 className="relative size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <p className="truncate text-[13px] font-semibold text-[#222222] dark:text-foreground sm:text-sm">
              {item.conversationTitle || item.turn.prompt || "未命名任务"}
            </p>
            <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1", tone.badgeClass)}>
              {getStatusLabel(item.finalStatus)}
            </span>
          </div>
          <p className="mt-1 line-clamp-2 text-xs leading-5 text-[#45515e] dark:text-muted-foreground">
            {item.turn.prompt || "无提示词内容"}
          </p>
          <div className="mt-3">
            <div className="mb-1.5 flex items-center justify-between gap-2 text-[11px] text-[#45515e] dark:text-muted-foreground">
              <span className="truncate font-medium text-[#222222] dark:text-foreground">{tone.message}</span>
              <span className="shrink-0 font-mono tabular-nums">{resultText}</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-[#edf2f7] dark:bg-muted">
              <div className={cn("h-full animate-pulse rounded-full", tone.barClass)} style={{ width: "100%" }} />
            </div>
          </div>
        </div>
      </div>
    </button>
  );
}

export function ImageTaskQueue({ className }: { className?: string }) {
  const navigate = useNavigate();
  const conversations = useImageConversationsForQueue();
  const progressByTurnKey = useSyncExternalStore(
    subscribeImageTurnProgress,
    getImageTurnProgressSnapshot,
    getImageTurnProgressSnapshot,
  );
  const queueItems = useMemo(() => getTaskQueueItems(conversations), [conversations]);
  const activeCount = queueItems.length;
  const [recentCompletions, setRecentCompletions] = useState<RecentQueueCompletion[]>([]);
  const [open, setOpen] = useState(false);
  const [now, setNow] = useState(Date.now());
  const previousQueueItemsRef = useRef<Map<string, TaskQueueItem>>(new Map());
  const hasRecentCompletion = recentCompletions.length > 0;

  useEffect(() => {
    const currentItems = new Map(queueItems.map((item) => [`${item.conversationId}:${item.turn.id}`, item]));
    const completedItems: RecentQueueCompletion[] = [];

    previousQueueItemsRef.current.forEach((previousItem, key) => {
      if (currentItems.has(key)) {
        return;
      }

      const completedItem = findQueueItem(conversations, previousItem.conversationId, previousItem.turn.id);
      if (!completedItem || isTurnBusy(completedItem.turn) || !isTerminalTurnStatus(completedItem.turn.status)) {
        return;
      }

      completedItems.push({
        ...completedItem,
        key,
        completedAt: Date.now(),
        finalStatus: completedItem.turn.status,
      });
    });

    previousQueueItemsRef.current = currentItems;
    if (completedItems.length > 0) {
      setRecentCompletions((current) => [...completedItems, ...current].slice(0, 3));
    }
  }, [conversations, queueItems]);

  useEffect(() => {
    if (recentCompletions.length === 0) {
      return;
    }

    const timer = window.setTimeout(() => {
      const cutoff = Date.now() - 4500;
      setRecentCompletions((current) => current.filter((item) => item.completedAt >= cutoff));
    }, 4500);
    return () => {
      window.clearTimeout(timer);
    };
  }, [recentCompletions]);

  useEffect(() => {
    if (!open && activeCount === 0 && Object.keys(progressByTurnKey).length === 0 && !hasRecentCompletion) {
      return;
    }

    setNow(Date.now());
    const timer = window.setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(timer);
    };
  }, [activeCount, hasRecentCompletion, open, progressByTurnKey]);

  const handleOpenConversation = (conversationId: string) => {
    window.localStorage.setItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY, conversationId);
    window.dispatchEvent(
      new CustomEvent(IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT, {
        detail: { conversationId },
      }),
    );
    setOpen(false);
    navigate("/image");
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className={cn(
            "relative h-9 rounded-full px-2.5 shadow-none",
            activeCount > 0
              ? "border-sky-200 bg-sky-50 text-[#1456f0] hover:bg-sky-100 dark:border-sky-800 dark:bg-sky-950/30 dark:text-sky-300"
              : hasRecentCompletion
                ? "border-emerald-200 bg-emerald-50 text-emerald-700 hover:bg-emerald-100 dark:border-emerald-800 dark:bg-emerald-950/30 dark:text-emerald-300"
              : "border-border bg-background text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            className,
          )}
          aria-label={activeCount > 0 ? `查看 ${activeCount} 个处理中任务` : hasRecentCompletion ? "查看刚完成的任务" : "查看任务队列"}
          title="任务队列"
        >
          {hasRecentCompletion ? (
            <span className="pointer-events-none absolute -inset-1 rounded-full border border-emerald-300/70 animate-ping" />
          ) : null}
          {activeCount > 0 ? (
            <LoaderCircle className="size-4 animate-spin" />
          ) : hasRecentCompletion ? (
            <CheckCircle2 className="size-4 animate-in zoom-in-50 duration-300" />
          ) : (
            <ClipboardList className="size-4" />
          )}
          <span className="hidden text-xs font-medium xl:inline">任务队列</span>
          {activeCount > 0 ? (
            <span className="absolute -top-1 -right-1 inline-flex min-w-5 items-center justify-center rounded-full bg-[#1456f0] px-1.5 text-[10px] font-semibold leading-5 text-white">
              {activeCount}
            </span>
          ) : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" sideOffset={8} className="w-[min(calc(100vw-1rem),460px)] p-0">
        <div className="flex items-center justify-between gap-3 border-b border-[#f2f3f5] px-3.5 py-2.5 dark:border-border sm:px-4 sm:py-3">
          <div className="flex min-w-0 items-center gap-2">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300">
              <ClipboardList className="size-4" />
            </span>
            <div className="min-w-0">
              <div className="text-sm font-semibold text-[#222222] dark:text-foreground">任务处理队列</div>
              <div className="text-xs text-[#8e8e93] dark:text-muted-foreground">
                {activeCount > 0
                  ? `${activeCount} 个任务正在排队或处理`
                  : hasRecentCompletion
                    ? "最近完成的任务"
                    : "暂无处理中任务"}
              </div>
            </div>
          </div>
        </div>

        {queueItems.length > 0 || recentCompletions.length > 0 ? (
          <div aria-live="polite" className="max-h-[min(58vh,460px)] overflow-y-auto bg-[#fbfcfe] p-2.5 dark:bg-background sm:max-h-[min(68vh,560px)] sm:p-3">
            <div className="flex flex-col gap-2.5 sm:gap-3">
              {recentCompletions.map((item) => (
                <CompletionItem
                  key={`${item.key}:${item.completedAt}`}
                  item={item}
                  onOpenConversation={handleOpenConversation}
                />
              ))}
              {queueItems.map((item) => (
                <QueueItem
                  key={`${item.conversationId}:${item.turn.id}`}
                  item={item}
                  now={now}
                  onOpenConversation={handleOpenConversation}
                />
              ))}
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center px-5 py-7 text-center sm:px-6 sm:py-10">
            <span className="flex size-10 items-center justify-center rounded-full bg-[#f0f0f0] text-[#45515e] dark:bg-muted dark:text-muted-foreground sm:size-12">
              <CheckCircle2 className="size-5" />
            </span>
            <div className="mt-3 text-sm font-semibold text-[#222222] dark:text-foreground">队列为空</div>
            <div className="mt-1 max-w-[260px] text-xs leading-5 text-[#8e8e93] dark:text-muted-foreground">
              在创作台提交图片或对话任务后，这里会显示对应的处理详情和进度。
            </div>
            <Button
              type="button"
              size="sm"
              className="mt-3 h-8 rounded-full bg-[#1456f0] px-3 text-xs text-white hover:bg-[#2563eb] sm:mt-4"
              onClick={() => {
                setOpen(false);
                navigate("/image");
              }}
            >
              <Sparkles className="size-3.5" />
              打开创作台
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
