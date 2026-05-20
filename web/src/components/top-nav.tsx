"use client";

import { lazy, Suspense, useEffect, useState } from "react";
import { ChevronDown, ChevronUp, Crown, LogOut, MoonStar, Sun, UserCircle2 } from "lucide-react";
import { motion, useReducedMotion, type Transition } from "motion/react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";

import webConfig from "@/constants/common-env";
import {
  AUTH_SESSION_CHANGE_EVENT,
  clearVerifiedAuthSession,
  getCachedAuthSession,
  getVerifiedAuthSession,
} from "@/lib/session";
import {
  canAccessPath,
  hasAPIPermission,
  type StoredAuthSession,
} from "@/store/auth";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { fetchAccounts, logout, type Account } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  applyColorTheme,
  getPreferredColorTheme,
  saveColorTheme,
  type ColorTheme,
} from "@/lib/theme";

const CheckinWidget = lazy(() => import("@/components/checkin-widget").then((module) => ({ default: module.CheckinWidget })));
const ImageTaskQueue = lazy(() => import("@/components/image-task-queue").then((module) => ({ default: module.ImageTaskQueue })));
const NotificationBell = lazy(() => import("@/components/notification-bell").then((module) => ({ default: module.NotificationBell })));

const navItems = [
  { href: "/image", label: "创作台" },
  { href: "/ecommerce-agent", label: "电商AI-Agent" },
  { href: "/accounts", label: "号池管理" },
  { href: "/register", label: "注册机" },
  { href: "/image-manager", label: "图片库" },
  { href: "/subscription", label: "订阅" },
  { href: "/invite", label: "邀请中心" },
  { href: "/users", label: "用户管理" },
  { href: "/rbac", label: "角色权限" },
  { href: "/logs", label: "日志管理" },
  { href: "/settings", label: "设置" },
];
const profileNavItem = { href: "/profile", label: "个人中心" };
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";
const PRIMARY_NAV_ID = "primary-navigation";
const NAV_ACTIVE_LAYOUT_ID = "top-nav-active-pill";
const navActiveTransition: Transition = {
  type: "spring",
  stiffness: 520,
  damping: 42,
  mass: 0.7,
};
const reducedNavActiveTransition: Transition = {
  duration: 0.01,
};

function formatAvailableQuota(accounts: Account[]) {
  const availableAccounts = accounts.filter((account) => account.status !== "禁用");
  return String(availableAccounts.reduce((sum, account) => sum + Math.max(0, account.quota), 0));
}

function formatUserQuota(session: StoredAuthSession | null | undefined) {
  if (!session || session.role !== "user") {
    return "--";
  }
  if (session.imageQuotaTotal === null || session.imageQuotaTotal === undefined) {
    return "不限";
  }
  const remaining =
    session.imageQuotaRemaining === null || session.imageQuotaRemaining === undefined
      ? Math.max(0, session.imageQuotaTotal - (session.imageQuotaUsed || 0))
      : Math.max(0, session.imageQuotaRemaining);
  return String(remaining);
}

function NavActionFallback({ className }: { className?: string }) {
  return <span className={cn("inline-flex size-8 shrink-0 rounded-full bg-black/[0.04] dark:bg-white/10", className)} />;
}

function ThemeToggleButton({
  theme,
  onToggle,
  className,
}: {
  theme: ColorTheme;
  onToggle: (button: HTMLButtonElement) => void;
  className?: string;
}) {
  const dark = theme === "dark";

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={cn("relative size-8 rounded-full", className)}
      onClick={(event) => onToggle(event.currentTarget)}
      aria-label={dark ? "切换到浅色模式" : "切换到深色模式"}
      title={dark ? "浅色模式" : "深色模式"}
    >
      <Sun className="scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
      <MoonStar className="absolute scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
      <span className="sr-only">切换界面主题</span>
    </Button>
  );
}

function DeferredNavActions({
  canAccessImageTasks,
  isAdminNav,
  session,
  mobile,
}: {
  canAccessImageTasks: boolean;
  isAdminNav: boolean;
  session: StoredAuthSession;
  mobile?: boolean;
}) {
  if (mobile) {
    return (
      <>
        <Suspense fallback={<NavActionFallback className="size-8 shrink-0" />}>
          {canAccessImageTasks ? <ImageTaskQueue className="size-8 shrink-0 px-0" /> : null}
        </Suspense>
        <Suspense fallback={<NavActionFallback className="size-8 shrink-0" />}>
          <NotificationBell announcementTarget="image" className="size-8 shrink-0" autoAnnouncementDialog />
        </Suspense>
      </>
    );
  }

  return (
    <>
      <Suspense fallback={canAccessImageTasks ? <NavActionFallback className={isAdminNav ? "xl:w-8" : undefined} /> : null}>
        {canAccessImageTasks ? <ImageTaskQueue className={isAdminNav ? "size-8 px-0 xl:w-8" : undefined} /> : null}
      </Suspense>
      <Suspense fallback={<NavActionFallback className={isAdminNav ? "h-8 w-14" : "h-8 w-20"} />}>
        <CheckinWidget session={session} className={isAdminNav ? "h-8 px-2" : undefined} />
      </Suspense>
      <Suspense fallback={<NavActionFallback />}>
        <NotificationBell announcementTarget="image" className="size-8" autoAnnouncementDialog />
      </Suspense>
    </>
  );
}

type NavItem = {
  href: string;
  label: string;
};

function isActivePath(pathname: string, href: string) {
  return pathname === href || pathname.startsWith(`${href}/`);
}

function NavPill({ item, pathname, compact }: { item: NavItem; pathname: string; compact?: boolean }) {
  const active = isActivePath(pathname, item.href);
  const prefersReducedMotion = useReducedMotion();

  return (
    <NavLink
      to={item.href}
      className={() =>
        cn(
          "relative isolate shrink-0 whitespace-nowrap rounded-full px-2.5 py-1 text-[12px] font-semibold transition-colors sm:text-[13px]",
          compact && "px-1.5 py-0.5 text-[11px] sm:text-[11px] xl:px-2",
          active
            ? "text-[#18181b] dark:text-accent-foreground"
            : "text-[#45515e] hover:bg-black/[0.05] hover:text-[#18181b] dark:text-muted-foreground dark:hover:bg-accent dark:hover:text-accent-foreground",
        )
      }
    >
      {active ? (
        <motion.span
          layoutId={NAV_ACTIVE_LAYOUT_ID}
          transition={prefersReducedMotion ? reducedNavActiveTransition : navActiveTransition}
          className="absolute inset-0 -z-10 rounded-full bg-[#f0f4ff] shadow-[inset_0_-1px_0_rgba(20,86,240,0.18),inset_0_0_0_1px_rgba(20,86,240,0.16)] dark:bg-accent"
        />
      ) : null}
      <motion.span
        animate={{ scale: active && !prefersReducedMotion ? 1.03 : 1 }}
        transition={prefersReducedMotion ? reducedNavActiveTransition : { duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
        className="relative z-10 block"
      >
        {item.label}
      </motion.span>
    </NavLink>
  );
}

function AccountMenu({
  session,
  roleLabel,
  availableQuota,
  pathname,
  onLogout,
}: {
  session: StoredAuthSession;
  roleLabel: string;
  availableQuota: string;
  pathname: string;
  onLogout: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const displayName = session.name || roleLabel;
  const initial = (displayName.trim() || "U").slice(0, 1).toUpperCase();
  const profileActive = isActivePath(pathname, profileNavItem.href);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className={cn(
            "h-8 rounded-full px-2 shadow-none",
            profileActive ? "border-[#1456f0]/30 bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300" : "",
          )}
          aria-label="账号菜单"
        >
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
            {initial}
          </span>
          <span className="hidden max-w-[120px] truncate lg:inline">{displayName}</span>
          <ChevronDown />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        sideOffset={8}
        className="w-64 rounded-[18px] border-border bg-card p-2 text-card-foreground shadow-[0_20px_60px_-30px_rgba(15,23,42,0.45)] dark:border-border dark:bg-card"
      >
        <div className="flex flex-col gap-2">
            <div className="rounded-xl bg-muted/50 p-2.5">
            <div className="flex min-w-0 items-center gap-3">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
                {initial}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-semibold text-foreground">{displayName}</div>
                <code className="block truncate font-mono text-xs text-muted-foreground">
                  {session.subjectId || session.role}
                </code>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-1.5 text-[11px]">
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">角色</div>
              <div className="truncate font-medium text-foreground">{roleLabel}</div>
            </div>
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">额度</div>
              <div className="truncate font-medium text-foreground">{availableQuota}</div>
            </div>
            <div className="rounded-lg bg-muted/40 px-2 py-1.5">
              <div className="text-muted-foreground">版本</div>
              <div className="truncate font-medium text-foreground">v{webConfig.appVersion}</div>
            </div>
          </div>

          <Link
            to={profileNavItem.href}
            className={cn(
              "flex items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium transition hover:bg-accent hover:text-accent-foreground",
              profileActive ? "bg-[#edf4ff] text-[#1456f0] dark:bg-sky-950/30 dark:text-sky-300" : "text-foreground",
            )}
            onClick={() => setOpen(false)}
          >
            <UserCircle2 className="size-4" />
            个人中心
          </Link>

          <button
            type="button"
            className="flex items-center justify-center gap-2 rounded-xl px-3 py-2 text-sm font-medium text-rose-600 transition hover:bg-rose-50 hover:text-rose-700 dark:hover:bg-rose-950/30"
            onClick={() => {
              setOpen(false);
              void onLogout();
            }}
          >
            <LogOut className="size-4" />
            退出登录
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

export function TopNav() {
  const location = useLocation();
  const navigate = useNavigate();
  const pathname = location.pathname.replace(/\/+$/, "") || "/";
  const [session, setSession] = useState<StoredAuthSession | null | undefined>(() => getCachedAuthSession());
  const [theme, setTheme] = useState<ColorTheme>(() => getPreferredColorTheme());
  const [availableQuota, setAvailableQuota] = useState("--");
  const [navCollapsed, setNavCollapsed] = useState(false);

  useEffect(() => {
    let active = true;

    const load = async () => {
      if (pathname === "/login") {
        if (!active) {
          return;
        }
        setSession(null);
        return;
      }

      const storedSession = await getVerifiedAuthSession();
      if (!active) {
        return;
      }
      setSession(storedSession);
    };

    void load();
    return () => {
      active = false;
    };
  }, [pathname]);

  useEffect(() => {
    const handleSessionChange = () => {
      setSession(getCachedAuthSession() ?? null);
    };
    window.addEventListener(AUTH_SESSION_CHANGE_EVENT, handleSessionChange);
    return () => {
      window.removeEventListener(AUTH_SESSION_CHANGE_EVENT, handleSessionChange);
    };
  }, []);

  useEffect(() => {
    if (session?.role === "user") {
      setAvailableQuota(formatUserQuota(session));
      return;
    }

    if (!hasAPIPermission(session, "GET", "/api/accounts")) {
      setAvailableQuota("--");
      return;
    }

    let active = true;
    const loadQuota = async () => {
      try {
        const data = await fetchAccounts();
        if (active) {
          setAvailableQuota(formatAvailableQuota(data.items));
        }
      } catch {
        if (active) {
          setAvailableQuota((current) => (current === "加载中..." ? "--" : current));
        }
      }
    };
    const handleRefresh = () => {
      void loadQuota();
    };

    setAvailableQuota("加载中...");
    void loadQuota();
    window.addEventListener("focus", handleRefresh);
    window.addEventListener(QUOTA_REFRESH_EVENT, handleRefresh);
    return () => {
      active = false;
      window.removeEventListener("focus", handleRefresh);
      window.removeEventListener(QUOTA_REFRESH_EVENT, handleRefresh);
    };
  }, [session]);

  const handleLogout = async () => {
    try {
      await logout();
    } catch {
      // Local logout should still complete if the server session cookie is already gone.
    }
    await clearVerifiedAuthSession();
    navigate("/login", { replace: true });
  };

  const handleThemeToggle = (button: HTMLButtonElement) => {
    const nextTheme = theme === "dark" ? "light" : "dark";
    const rect = button.getBoundingClientRect();
    applyColorTheme(
      nextTheme,
      {
        force: true,
        origin: {
          x: rect.left + rect.width / 2,
          y: rect.top + rect.height / 2,
        },
      },
    );
    saveColorTheme(nextTheme);
    setTheme(nextTheme);
  };

  if (pathname === "/login" || pathname === "/auth/linuxdo/callback" || session === undefined || !session) {
    return null;
  }

  const visibleNavItems = navItems.filter((item) => canAccessPath(session, item.href));
  const roleLabel = session.role === "admin" ? "管理员" : session.roleName || (session.provider === "linuxdo" ? "Linuxdo 用户" : "普通用户");
  const canAccessImageTasks = canAccessPath(session, "/image");
  const isAdminNav = session.role === "admin";
  const navToggleLabel = navCollapsed ? "展开导航栏" : "收起导航栏";

  return (
    <header className="sticky top-2 z-40 rounded-[20px] border border-white/80 bg-white/[0.86] shadow-[0_14px_44px_-30px_rgba(21,36,76,0.45)] backdrop-blur-2xl dark:border-border dark:bg-card/92">
      <div className="flex min-h-12 flex-col gap-1.5 px-2.5 py-1.5 lg:flex-row lg:items-center lg:justify-between lg:gap-3 lg:px-3">
        <div className="flex min-w-0 items-center justify-between gap-2 lg:justify-start">
          <Button
            type="button"
            variant="ghost"
            className={cn(
              "font-display h-8 max-w-[150px] justify-start rounded-full px-1 pr-2 text-[14px] font-semibold text-[#18181b] shadow-none hover:bg-black/[0.04] hover:text-[#1456f0] sm:max-w-[190px] lg:max-w-none dark:text-foreground dark:hover:text-sky-300",
              navCollapsed ? "bg-black/[0.04] text-[#1456f0] dark:bg-accent dark:text-sky-300" : "",
            )}
            aria-controls={PRIMARY_NAV_ID}
            aria-expanded={!navCollapsed}
            aria-label={navToggleLabel}
            title={navToggleLabel}
            onClick={() => setNavCollapsed((collapsed) => !collapsed)}
          >
            <img
              src="/logo-1818.svg"
              alt="1818"
              className="h-7 w-[100px] shrink-0 sm:w-[112px]"
            />
            <span className="sr-only">1818</span>
            {navCollapsed ? <ChevronDown aria-hidden="true" /> : <ChevronUp aria-hidden="true" />}
          </Button>
          <div className="hide-scrollbar ml-auto flex min-w-0 max-w-[calc(100vw-11rem)] flex-1 items-center justify-end gap-0.5 overflow-x-auto overscroll-x-contain lg:hidden">
            <DeferredNavActions canAccessImageTasks={canAccessImageTasks} isAdminNav={isAdminNav} session={session} mobile />
            <ThemeToggleButton theme={theme} onToggle={handleThemeToggle} />
            <AccountMenu
              session={session}
              roleLabel={roleLabel}
              availableQuota={availableQuota}
              pathname={pathname}
              onLogout={handleLogout}
            />
          </div>
        </div>
        <nav
          id={PRIMARY_NAV_ID}
          aria-label="主导航"
          className={cn(
            "hide-scrollbar -mx-1 min-w-0 gap-0.5 overflow-x-auto overscroll-x-contain px-1 pb-0.5 scroll-px-1 touch-pan-x [-webkit-overflow-scrolling:touch] lg:mx-0 lg:flex-1 lg:justify-center lg:gap-1 lg:px-0 lg:pb-0",
            isAdminNav && "lg:gap-0.5 xl:gap-1",
            navCollapsed ? "hidden" : "flex",
          )}
        >
          {visibleNavItems.map((item) => (
            <NavPill key={item.href} item={item} pathname={pathname} compact={isAdminNav} />
          ))}
        </nav>
        <div className={cn("hidden items-center justify-end gap-1 lg:flex", isAdminNav && "gap-0.5")}>
          <DeferredNavActions canAccessImageTasks={canAccessImageTasks} isAdminNav={isAdminNav} session={session} />
          <ThemeToggleButton theme={theme} onToggle={handleThemeToggle} />
          <Button
            asChild
            className="hidden h-8 rounded-full bg-[#1f43ee] px-3 text-xs font-bold text-white shadow-[0_10px_22px_-14px_rgba(31,67,238,0.8)] hover:bg-[#1637d8] xl:inline-flex"
          >
            <Link to="/subscription">
              <Crown className="size-3.5" />
              升级 Pro
            </Link>
          </Button>
          <AccountMenu
            session={session}
            roleLabel={roleLabel}
            availableQuota={availableQuota}
            pathname={pathname}
            onLogout={handleLogout}
          />
        </div>
      </div>
    </header>
  );
}
