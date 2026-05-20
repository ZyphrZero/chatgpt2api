"use client";

import { useEffect, useMemo, useState } from "react";
import { LoaderCircle, MessageSquarePlus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { getImageConversationStats, type ImageConversation } from "@/store/image-conversations";

type ImageSidebarProps = {
  conversations: ImageConversation[];
  isLoadingHistory: boolean;
  selectedConversationId: string | null;
  onCreateDraft: () => void;
  onClearHistory: () => void | Promise<void>;
  onSelectConversation: (id: string) => void;
  onDeleteConversation: (id: string) => void | Promise<void>;
  formatConversationTime: (value: string) => string;
  hideActionButtons?: boolean;
  initialVisibleCount?: number;
  loadMoreCount?: number;
};

export function ImageSidebar({
  conversations,
  isLoadingHistory,
  selectedConversationId,
  onCreateDraft,
  onClearHistory,
  onSelectConversation,
  onDeleteConversation,
  formatConversationTime,
  hideActionButtons = false,
  initialVisibleCount,
  loadMoreCount,
}: ImageSidebarProps) {
  const [visibleCount, setVisibleCount] = useState(initialVisibleCount ?? 0);
  const shouldLimitConversations = typeof initialVisibleCount === "number" && initialVisibleCount > 0;
  const nextLoadCount = Math.max(1, loadMoreCount ?? initialVisibleCount ?? 40);
  const visibleConversations = useMemo(
    () => shouldLimitConversations ? conversations.slice(0, visibleCount) : conversations,
    [conversations, shouldLimitConversations, visibleCount],
  );
  const hiddenConversationCount = shouldLimitConversations
    ? Math.max(0, conversations.length - visibleConversations.length)
    : 0;

  useEffect(() => {
    if (shouldLimitConversations) {
      setVisibleCount(initialVisibleCount);
    }
  }, [conversations.length, initialVisibleCount, shouldLimitConversations]);

  return (
    <aside className="h-full min-h-0 overflow-hidden">
      <div className="flex h-full min-h-0 flex-col gap-2 py-0.5 sm:gap-2">
        {!hideActionButtons && (
          <div className="flex items-center gap-2">
            <Button className="h-9 flex-1 rounded-[16px] text-[13px] dark:bg-[#d6aa56] dark:text-[#100b04] dark:hover:bg-[#e7bf6d]" onClick={onCreateDraft}>
              <MessageSquarePlus className="size-4" />
              新建对话
            </Button>
            <Button
              variant="outline"
              className="h-9 rounded-[16px] border-[#e5e7eb] bg-white px-3 text-[#45515e] hover:bg-black/[0.05] dark:border-[#d6aa56]/28 dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#20180d]"
              onClick={() => void onClearHistory()}
              disabled={conversations.length === 0}
            >
              <Trash2 className="size-4" />
            </Button>
          </div>
        )}

        <div
          className={cn(
            "min-h-0 flex-1 overflow-y-auto [scrollbar-color:rgba(142,142,147,.45)_transparent] [scrollbar-width:thin] dark:[scrollbar-color:rgba(214,170,86,.38)_transparent] [&::-webkit-scrollbar]:w-1.5 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-[#8e8e93]/45 [&::-webkit-scrollbar-track]:bg-transparent dark:[&::-webkit-scrollbar-thumb]:bg-[#d6aa56]/35",
            hideActionButtons ? "flex flex-col gap-1 pr-0" : "flex flex-col gap-2 pr-1",
          )}
        >
          {isLoadingHistory ? (
            <div className="flex items-center gap-2 px-2 py-3 text-sm text-stone-500">
              <LoaderCircle className="size-4 animate-spin" />
              <span>正在读取会话记录</span>
            </div>
          ) : conversations.length === 0 ? (
            <div className="px-2 py-3 text-sm leading-6 text-stone-500">还没有图片记录，输入提示词后会在这里显示。</div>
          ) : (
            <>
            {shouldLimitConversations ? (
              <div className="px-1 pb-1 text-xs text-[#8e8e93] dark:text-[#7c704f]">
                已显示 {visibleConversations.length} / {conversations.length} 条历史
              </div>
            ) : null}
            {visibleConversations.map((conversation) => {
              const active = conversation.id === selectedConversationId;
              const stats = getImageConversationStats(conversation);
              return (
                <div
                  key={conversation.id}
                  className={cn(
                    "group relative w-full rounded-[15px] border text-left transition",
                    hideActionButtons ? "px-3.5 py-3" : "px-2.5 py-2",
                    active
                      ? "border-[#f2f3f5] bg-white text-[#18181b] shadow-[0_4px_6px_rgba(0,0,0,0.08)] dark:border-[#d6aa56]/42 dark:bg-[#15120d] dark:text-[#f5e6bf] dark:shadow-[0_14px_32px_-24px_rgba(214,170,86,0.70),inset_0_1px_0_rgba(255,224,166,0.12)]"
                      : "border-transparent text-[#45515e] hover:border-[#f2f3f5] hover:bg-white dark:text-[#9b875c] dark:hover:border-[#d6aa56]/24 dark:hover:bg-[#15120d]/76",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => onSelectConversation(conversation.id)}
                    className={cn("block w-full text-left", hideActionButtons ? "pr-0" : "pr-8")}
                  >
                    <div className={cn("truncate font-semibold", hideActionButtons ? "text-[15px]" : "text-[13px]")}>
                      <span className="truncate">{conversation.title}</span>
                    </div>
                    <div className={cn("mt-0.5 text-[11px]", active ? "text-[#45515e] dark:text-[#d6aa56]/85" : "text-[#8e8e93] dark:text-[#7c704f]")}>
                      {conversation.turns.length} 轮 · {formatConversationTime(conversation.updatedAt)}
                    </div>
                    {stats.running > 0 || stats.queued > 0 ? (
                      <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[10px]">
                        {stats.running > 0 ? (
                          <span className="rounded-full bg-blue-50 px-2 py-1 text-blue-600">处理中 {stats.running}</span>
                        ) : null}
                        {stats.queued > 0 ? (
                          <span className="rounded-full bg-amber-50 px-2 py-1 text-amber-700">排队 {stats.queued}</span>
                        ) : null}
                      </div>
                    ) : null}
                  </button>
                  {!hideActionButtons ? (
                    <button
                      type="button"
                      onClick={() => void onDeleteConversation(conversation.id)}
                      className="absolute top-3 right-2 inline-flex size-7 items-center justify-center rounded-md text-stone-400 opacity-0 transition hover:bg-stone-100 hover:text-rose-500 group-hover:opacity-100 dark:text-[#9b875c] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                      aria-label="删除会话"
                    >
                      <Trash2 className="size-4" />
                    </button>
                  ) : null}
                </div>
              );
            })}
            {hiddenConversationCount > 0 ? (
              <button
                type="button"
                className="rounded-[15px] border border-dashed border-[#d7dce4] px-3.5 py-2.5 text-center text-xs font-semibold text-[#45515e] transition hover:border-[#b9c1cd] hover:bg-white dark:border-[#d6aa56]/22 dark:text-[#d6aa56] dark:hover:bg-[#15120d]/76"
                onClick={() => setVisibleCount((current) => Math.min(conversations.length, current + nextLoadCount))}
              >
                显示更多历史（再显示 {Math.min(nextLoadCount, hiddenConversationCount)} 条，剩余 {hiddenConversationCount} 条）
              </button>
            ) : null}
            </>
          )}
        </div>
      </div>
    </aside>
  );
}
