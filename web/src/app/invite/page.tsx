"use client";

import { useCallback, useEffect, useState } from "react";
import { Copy, Gift, LoaderCircle, RefreshCw, Share2, UsersRound } from "lucide-react";
import { toast } from "sonner";

import { INVITE_REFRESH_EVENT } from "@/components/notification-bell";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchMyInvite, type InviteSummary } from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";

function formatTime(value?: string) {
  if (!value) {
    return "--";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

export default function InvitePage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/invite");
  const [summary, setSummary] = useState<InviteSummary | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);

  const loadSummary = useCallback(
    async (mode: "initial" | "refresh") => {
      if (mode === "refresh") {
        setIsRefreshing(true);
      }
      try {
        const data = await fetchMyInvite();
        setSummary(data);
      } catch (error) {
        // Silent refreshes should not spam the toast layer with the same error.
        if (mode === "initial") {
          toast.error(error instanceof Error ? error.message : "读取邀请信息失败");
        }
      } finally {
        if (mode === "initial") {
          setIsLoading(false);
        }
        if (mode === "refresh") {
          setIsRefreshing(false);
        }
      }
    },
    [],
  );

  useEffect(() => {
    if (!session) {
      return;
    }
    void loadSummary("initial");
  }, [loadSummary, session]);

  // Reload when the notification bell tells us a new invite_reward / invitee_bonus arrived,
  // when the tab regains focus, or when the user pulls the page back into view. This
  // keeps the invite stats in sync with the bell badge without polling.
  useEffect(() => {
    if (!session) {
      return;
    }
    const handleRefresh = () => {
      void loadSummary("refresh");
    };
    const handleVisibility = () => {
      if (document.visibilityState === "visible") {
        void loadSummary("refresh");
      }
    };
    window.addEventListener(INVITE_REFRESH_EVENT, handleRefresh);
    window.addEventListener("focus", handleRefresh);
    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      window.removeEventListener(INVITE_REFRESH_EVENT, handleRefresh);
      window.removeEventListener("focus", handleRefresh);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, [loadSummary, session]);

  const handleCopy = async () => {
    if (!summary?.invite_url) {
      return;
    }
    await copyText(summary.invite_url);
    toast.success("邀请链接已复制");
  };

  const handleNativeShare = async () => {
    if (!summary?.invite_url) {
      return;
    }
    const text = `我在 1818 生成图片，送你 ${summary.invitee_bonus_quota} 张体验额度：${summary.invite_url}`;
    if (navigator.share) {
      try {
        await navigator.share({ title: "1818 邀请", text, url: summary.invite_url });
        return;
      } catch {
        // Fall back to copy below.
      }
    }
    await copyText(text);
    toast.success("邀请文案已复制");
  };

  if (isCheckingAuth || !session || isLoading) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
      <PageHeader
        eyebrow="INVITE"
        title="邀请中心"
        actions={
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              onClick={() => void loadSummary("refresh")}
              disabled={!session || isRefreshing}
              title="刷新邀请数据"
            >
              <RefreshCw className={`size-4 ${isRefreshing ? "animate-spin" : ""}`} />
              {isRefreshing ? "刷新中" : "刷新"}
            </Button>
            <Button variant="outline" onClick={() => void handleCopy()} disabled={!summary}>
              <Copy className="size-4" />
              复制链接
            </Button>
            <Button onClick={() => void handleNativeShare()} disabled={!summary}>
              <Share2 className="size-4" />
              分享邀请
            </Button>
          </div>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1.2fr_0.8fr]">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-2xl">
              <Gift className="size-5 text-[#1456f0]" />
              专属邀请链接
            </CardTitle>
            <CardDescription>通过该页面注册的新用户会强制绑定你的邀请码，并按后台设置发放奖励。</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="rounded-2xl border border-border bg-muted/40 p-4">
              <div className="text-xs font-medium text-muted-foreground">邀请码</div>
              <div className="mt-1 break-all font-mono text-2xl font-semibold text-foreground">{summary?.invite_code || "--"}</div>
            </div>
            <div className="rounded-2xl border border-border bg-muted/40 p-4">
              <div className="text-xs font-medium text-muted-foreground">邀请链接</div>
              <div className="mt-1 break-all font-mono text-sm text-foreground">{summary?.invite_url || "--"}</div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-2xl">
              <UsersRound className="size-5 text-[#1456f0]" />
              奖励统计
            </CardTitle>
            <CardDescription>点数来自后台邀请奖励配置。</CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-3">
            <div className="rounded-2xl bg-muted/50 p-4">
              <div className="text-xs text-muted-foreground">已邀请</div>
              <div className="mt-1 text-3xl font-semibold">{summary?.invited_count ?? 0}</div>
            </div>
            <div className="rounded-2xl bg-muted/50 p-4">
              <div className="text-xs text-muted-foreground">累计奖励</div>
              <div className="mt-1 text-3xl font-semibold">{summary?.invite_reward_total ?? 0}</div>
            </div>
            <div className="rounded-2xl bg-muted/50 p-4">
              <div className="text-xs text-muted-foreground">邀请者每人</div>
              <div className="mt-1 text-3xl font-semibold">{summary?.invite_reward_quota ?? 0}</div>
            </div>
            <div className="rounded-2xl bg-muted/50 p-4">
              <div className="text-xs text-muted-foreground">被邀请额外</div>
              <div className="mt-1 text-3xl font-semibold">{summary?.invitee_bonus_quota ?? 0}</div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-xl">邀请记录</CardTitle>
          <CardDescription>最近通过你的邀请码注册的用户。</CardDescription>
        </CardHeader>
        <CardContent>
          {summary?.invited_users?.length ? (
            <div className="overflow-hidden rounded-2xl border border-border">
              {summary.invited_users.map((user, index) => (
                <div key={user.id || user.username || user.email || `${user.created_at || "invite"}-${index}`} className="grid gap-2 border-b border-border px-4 py-3 last:border-b-0 sm:grid-cols-[1fr_auto]">
                  <div>
                    <div className="font-medium text-foreground">{user.name || user.username || user.email || "未命名用户"}</div>
                    <div className="text-xs text-muted-foreground">奖励 {user.bonus_quota ?? summary.invite_reward_quota ?? 0} 张</div>
                  </div>
                  <div className="text-sm text-muted-foreground">{formatTime(user.created_at)}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-2xl border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
              还没有邀请记录。
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
