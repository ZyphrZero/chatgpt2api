"use client";

import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { KeyRound, LoaderCircle, MoonStar, ShieldCheck, Sun } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { loginWithAdminKey } from "@/lib/api";
import { setVerifiedAuthSession } from "@/lib/session";
import { applyColorTheme, getPreferredColorTheme, saveColorTheme, type ColorTheme } from "@/lib/theme";
import { useRedirectIfAuthenticated } from "@/lib/use-auth-guard";
import { getDefaultRouteForSession } from "@/store/auth";

const adminLoginBackgroundClass =
  "bg-[#f6f8fc] bg-[radial-gradient(rgba(20,86,240,0.12)_1px,transparent_1px),linear-gradient(145deg,#f7faff_0%,#ffffff_48%,#f6f6ff_100%)] [background-position:0_0,center] [background-size:12px_12px,cover] dark:bg-[#090d16] dark:bg-[radial-gradient(rgba(96,165,250,0.16)_1px,transparent_1px),linear-gradient(145deg,#080b13_0%,#101827_52%,#070b12_100%)]";

export default function AdminLoginPage() {
  const navigate = useNavigate();
  const themeToggleRef = useRef<HTMLButtonElement | null>(null);
  const [theme, setTheme] = useState<ColorTheme>(() => getPreferredColorTheme());
  const [key, setKey] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const { isCheckingAuth } = useRedirectIfAuthenticated();

  const handleThemeToggle = () => {
    const nextTheme = theme === "dark" ? "light" : "dark";
    const rect = themeToggleRef.current?.getBoundingClientRect();
    applyColorTheme(nextTheme, rect ? {
      origin: {
        x: rect.left + rect.width / 2,
        y: rect.top + rect.height / 2,
      },
    } : undefined);
    saveColorTheme(nextTheme);
    setTheme(nextTheme);
  };

  useEffect(() => {
    const redirect = new URLSearchParams(typeof window !== "undefined" ? window.location.search : "").get("redirect");
    if (redirect && redirect.startsWith("/login")) {
      navigate("/admin-login", { replace: true });
    }
  }, [navigate]);

  const handleSubmit = async () => {
    const normalized = key.trim();
    if (!normalized) {
      toast.error("请输入管理员密钥");
      return;
    }
    setIsSubmitting(true);
    try {
      const data = await loginWithAdminKey(normalized);
      const token = String(data.token || "").trim();
      if (!token) {
        throw new Error("管理员会话签发失败");
      }
      const session = {
        key: token,
        role: data.role,
        roleId: data.role_id,
        roleName: data.role_name,
        subjectId: data.subject_id,
        name: data.name,
        provider: data.provider,
        menuPaths: data.menu_paths || [],
        apiPermissions: data.api_permissions || [],
        menus: data.menus || [],
        imageQuotaTotal: data.image_quota_total ?? null,
        imageQuotaUsed: Math.max(0, Number(data.image_quota_used ?? 0) || 0),
        imageQuotaRemaining: data.image_quota_remaining ?? null,
      };
      await setVerifiedAuthSession(session);
      toast.success("管理员登录成功");
      navigate(getDefaultRouteForSession(session), { replace: true });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "管理员登录失败");
    } finally {
      setIsSubmitting(false);
    }
  };

  if (isCheckingAuth) {
    return (
      <div className={`${adminLoginBackgroundClass} fixed inset-0 z-50 grid min-h-svh w-screen place-items-center overflow-hidden px-4 py-6`}>
        <LoaderCircle className="size-5 animate-spin text-[#45515e] dark:text-white/60" />
      </div>
    );
  }

  return (
    <div className={`${adminLoginBackgroundClass} fixed inset-0 z-50 flex min-h-svh w-screen items-center justify-center overflow-y-auto px-4 py-6 font-login [align-items:safe_center] sm:px-6 lg:px-8`}>
      <div className="fixed right-4 top-4 z-50 flex items-center gap-2 sm:right-6 sm:top-6">
        <Button
          ref={themeToggleRef}
          type="button"
          variant="outline"
          size="icon"
          className="relative rounded-full border-border/60 bg-background/80 shadow-sm backdrop-blur"
          onClick={handleThemeToggle}
          aria-label={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"}
          title={theme === "dark" ? "浅色模式" : "深色模式"}
        >
          <Sun className="scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
          <MoonStar className="absolute scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
        </Button>
      </div>

      <div className="relative z-10 w-full max-w-md overflow-hidden rounded-[32px] border border-white/80 bg-white/95 p-8 shadow-[0_28px_80px_rgba(15,23,42,0.12)] backdrop-blur dark:border-white/10 dark:bg-[#111827]/92">
        <div className="flex flex-col gap-6">
          <div className="inline-flex w-fit items-center gap-2 rounded-full border border-[#dfe7f1] bg-white/80 px-3 py-1 text-[11px] font-semibold tracking-[0.2em] text-[#45515e] uppercase">
            <ShieldCheck className="size-3.5 text-[#1456f0]" />
            Admin Access
          </div>
          <div className="space-y-2">
            <h1 className="text-[2rem] leading-[1.12] font-semibold tracking-[-0.04em] text-[#222222] dark:text-white">
              管理员密钥登录
            </h1>
            <p className="text-sm leading-6 text-[#45515e] dark:text-white/62">
              普通用户已切换为 QQ 授权登录。管理员请使用密钥直登后台。
            </p>
          </div>

          <form
            className="flex flex-col gap-5"
            onSubmit={(event) => {
              event.preventDefault();
              void handleSubmit();
            }}
          >
            <div className="flex flex-col gap-2">
              <label htmlFor="admin-login-key" className="block text-sm font-semibold text-[#222222] dark:text-white/88">
                管理员密钥
              </label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-[#8e8e93] dark:text-white/42" />
                <Input
                  id="admin-login-key"
                  type="password"
                  autoComplete="current-password"
                  value={key}
                  onChange={(event) => setKey(event.target.value)}
                  placeholder="输入管理员密钥或管理员密码"
                  className="h-12 rounded-[16px] bg-white/90 pl-10 shadow-[0_6px_18px_rgba(24,40,72,0.05)] dark:border-white/12 dark:bg-white/8 dark:text-white dark:placeholder:text-white/38"
                />
              </div>
            </div>

            <div className="flex flex-col gap-3 pt-1">
              <Button
                type="submit"
                variant="outline"
                className="relative mx-auto h-12 w-full overflow-hidden rounded-[1.45rem] border-slate-300/85 bg-white/72 text-[#18181b] shadow-[0_12px_28px_rgba(148,163,184,0.18)] backdrop-blur-md"
                disabled={isSubmitting}
              >
                <span className="relative z-10 flex items-center gap-2 font-semibold tracking-[-0.01em]">
                  {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
                  登录后台
                </span>
              </Button>
              <Button type="button" variant="ghost" className="mx-auto h-10 w-full rounded-[1.2rem]" onClick={() => navigate("/login", { replace: true })}>
                返回用户登录
              </Button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
