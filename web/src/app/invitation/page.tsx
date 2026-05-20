"use client";

import { useEffect, useMemo, useState } from "react";
import { ArrowRight, Copy, Gift, LoaderCircle, ShieldCheck } from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchPublicInvite, type PublicInvite } from "@/lib/api";

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

export default function InvitationPage() {
  const location = useLocation();
  const inviteCode = useMemo(() => {
    const params = new URLSearchParams(location.search);
    return (params.get("code") || params.get("invite") || "").trim();
  }, [location.search]);
  const [invite, setInvite] = useState<PublicInvite | null>(null);
  const [isLoading, setIsLoading] = useState(Boolean(inviteCode));
  const [error, setError] = useState("");

  useEffect(() => {
    if (!inviteCode) {
      setError("邀请链接缺少邀请码");
      setIsLoading(false);
      return;
    }
    let active = true;
    const load = async () => {
      try {
        const data = await fetchPublicInvite(inviteCode);
        if (active) {
          setInvite(data);
          setError("");
        }
      } catch (requestError) {
        if (active) {
          setError(requestError instanceof Error ? requestError.message : "邀请链接不可用");
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
  }, [inviteCode]);

  const registerURL = `/login?invite=${encodeURIComponent(inviteCode)}`;

  return (
    <div className="fixed inset-0 z-40 min-h-svh overflow-y-auto bg-[#f8fafc] bg-[radial-gradient(rgba(20,86,240,0.12)_1px,transparent_1px),linear-gradient(145deg,#fff_0%,#f6f9ff_52%,#eef6ff_100%)] [background-size:14px_14px,cover] px-4 py-8 dark:bg-[#07111f] dark:bg-[radial-gradient(rgba(14,165,233,0.14)_1px,transparent_1px),linear-gradient(145deg,#07111f_0%,#0b1726_52%,#07111f_100%)]">
      <div className="mx-auto flex min-h-[calc(100svh-4rem)] w-full max-w-4xl items-center justify-center">
        <Card className="w-full overflow-hidden border-white/70 bg-white/94 shadow-[0_28px_80px_rgba(15,23,42,0.14)] dark:border-white/10 dark:bg-[#0f172a]/92">
          <CardHeader className="gap-5 p-8 sm:p-10">
            <div className="flex items-center gap-3">
              <img src="/logo-1818.svg" alt="1818" className="h-10 w-[140px]" />
              <div>
                <div className="text-sm font-semibold text-foreground">1818</div>
                <div className="text-xs uppercase tracking-[0.24em] text-muted-foreground">Invitation</div>
              </div>
            </div>
            <div className="max-w-2xl">
              <CardTitle className="text-4xl leading-tight sm:text-6xl">把想法变成作品</CardTitle>
              <CardDescription className="mt-4 text-base leading-7">
                通过邀请链接注册后，系统会自动绑定邀请码，并按后台配置发放注册赠送和邀请额外额度。
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent className="px-8 pb-8 sm:px-10 sm:pb-10">
            {isLoading ? (
              <div className="flex min-h-40 items-center justify-center">
                <LoaderCircle className="size-6 animate-spin text-muted-foreground" />
              </div>
            ) : error ? (
              <div className="rounded-3xl border border-rose-200 bg-rose-50 px-5 py-4 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/30 dark:text-rose-200">
                {error}
              </div>
            ) : (
              <div className="grid gap-4 lg:grid-cols-[1fr_auto] lg:items-end">
                <div className="rounded-3xl border border-border bg-muted/45 p-5">
                  <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Gift className="size-4 text-[#1456f0]" />
                    来自 {invite?.inviter_name || "好友"} 的邀请
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="rounded-2xl bg-background/80 p-4">
                      <div className="text-xs text-muted-foreground">邀请码</div>
                      <div className="mt-1 break-all font-mono text-xl font-semibold">{invite?.invite_code || inviteCode}</div>
                    </div>
                    <div className="rounded-2xl bg-background/80 p-4">
                      <div className="text-xs text-muted-foreground">你将额外获得</div>
                      <div className="mt-1 text-xl font-semibold">{invite?.invitee_bonus_quota ?? 0} 张图片额度</div>
                    </div>
                  </div>
                </div>
                <div className="flex flex-col gap-2 sm:flex-row lg:flex-col">
                  <Button asChild className="h-12 rounded-2xl px-6">
                    <Link to={registerURL}>
                      使用邀请码注册
                      <ArrowRight className="size-4" />
                    </Link>
                  </Button>
                  <Button
                    variant="outline"
                    className="h-12 rounded-2xl px-6"
                    onClick={() => {
                      void copyText(invite?.invite_url || window.location.href).then(() => toast.success("邀请链接已复制"));
                    }}
                  >
                    <Copy className="size-4" />
                    复制链接
                  </Button>
                </div>
              </div>
            )}
            <div className="mt-6 flex items-center gap-2 text-xs text-muted-foreground">
              <ShieldCheck className="size-4" />
              注册页会锁定当前邀请码，避免手动填错或丢失邀请关系。
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
