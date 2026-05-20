"use client";

import {
  CircleHelp,
  Copy,
  LoaderCircle,
  LogIn,
} from "lucide-react";
import { useMemo } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import webConfig from "@/constants/common-env";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  settingsDialogInputClassName,
  settingsInlineCodeClassName,
} from "./settings-ui";

const socialSectionClassName = "flex flex-col gap-3";
const socialFieldClassName = "gap-1.5";

function SocialTip({ content }: { content: string }) {
  return (
    <span
      aria-label={content}
      title={content}
      className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
    >
      <CircleHelp className="size-4" />
    </span>
  );
}

function SocialSectionHeading({
  tip,
  title,
}: {
  tip: string;
  title: string;
}) {
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <h3 className="truncate text-sm leading-6 font-semibold text-foreground">
        {title}
      </h3>
      <SocialTip content={tip} />
    </div>
  );
}

function trimTrailingSlash(value: string) {
  return value.trim().replace(/\/+$/, "");
}

function buildRedirectUrlSuggestion(baseUrl: string) {
  const configuredBaseUrl = trimTrailingSlash(baseUrl);
  if (configuredBaseUrl) {
    return `${configuredBaseUrl}/auth/social/oauth/callback`;
  }
  const apiUrl = trimTrailingSlash(webConfig.apiUrl || "");
  if (apiUrl) {
    return `${apiUrl}/auth/social/oauth/callback`;
  }
  if (typeof window === "undefined") {
    return "";
  }
  return `${window.location.origin}/auth/social/oauth/callback`;
}

function buildFrontendRedirectUrlSuggestion() {
  if (typeof window === "undefined") {
    return "/auth/social/callback";
  }
  return `${window.location.origin}/auth/social/callback`;
}

export function SocialLoginCard() {
  const config = useSettingsStore((state) => state.config);
  const isLoadingConfig = useSettingsStore((state) => state.isLoadingConfig);
  const setSocialLoginBaseUrl = useSettingsStore((state) => state.setSocialLoginBaseUrl);
  const setSocialLoginAppId = useSettingsStore((state) => state.setSocialLoginAppId);
  const setSocialLoginAppKey = useSettingsStore((state) => state.setSocialLoginAppKey);
  const setSocialLoginRedirectUrl = useSettingsStore((state) => state.setSocialLoginRedirectUrl);
  const setSocialLoginFrontendRedirectUrl = useSettingsStore((state) => state.setSocialLoginFrontendRedirectUrl);
  const setSocialLoginQQEnabled = useSettingsStore((state) => state.setSocialLoginQQEnabled);
  const setSocialLoginWXEnabled = useSettingsStore((state) => state.setSocialLoginWXEnabled);
  const setSocialLoginDouyinEnabled = useSettingsStore((state) => state.setSocialLoginDouyinEnabled);

  const redirectUrlSuggestion = useMemo(
    () => buildRedirectUrlSuggestion(String(config?.base_url || "")),
    [config?.base_url],
  );
  const frontendRedirectUrlSuggestion = useMemo(
    () => buildFrontendRedirectUrlSuggestion(),
    [],
  );

  const anyEnabled = Boolean(config?.social_login_qq_enabled || config?.social_login_wx_enabled || config?.social_login_douyin_enabled);
  const appKeyConfigured = Boolean(config?.social_login_app_key_configured);

  const handleUseSuggestedRedirectUrl = async () => {
    if (!redirectUrlSuggestion) return;
    setSocialLoginRedirectUrl(redirectUrlSuggestion);
    try {
      await navigator.clipboard.writeText(redirectUrlSuggestion);
      toast.success("聚合登录后端回调已填入并复制");
    } catch {
      toast.success("聚合登录后端回调已填入");
    }
  };

  const handleUseSuggestedFrontendRedirectUrl = async () => {
    if (!frontendRedirectUrlSuggestion) return;
    setSocialLoginFrontendRedirectUrl(frontendRedirectUrlSuggestion);
    try {
      await navigator.clipboard.writeText(frontendRedirectUrlSuggestion);
      toast.success("聚合登录前端完成页已填入并复制");
    } catch {
      toast.success("聚合登录前端完成页已填入");
    }
  };

  if (isLoadingConfig) {
    return (
      <SettingsCard
        icon={LogIn}
        title="聚合登录"
        description="接入同一套 APPID / APPKEY，同时开启 QQ、微信、抖音登录。"
        tone="violet"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard
      icon={LogIn}
      title="聚合登录"
      description="接入同一套 APPID / APPKEY，同时开启 QQ、微信、抖音登录。"
      tone="violet"
      action={
        <Badge variant={anyEnabled ? "success" : "secondary"}>
          {anyEnabled ? "聚合登录已启用" : "聚合登录未启用"}
        </Badge>
      }
    >
      <div className="flex flex-col gap-5">
        <section className={socialSectionClassName}>
          <SocialSectionHeading
            title="基础配置"
            tip={
              appKeyConfigured
                ? "聚合登录使用同一套接口地址、APPID 和 APPKEY；APPKEY 已配置，留空会保留当前密钥。"
                : "聚合登录使用同一套接口地址、APPID 和 APPKEY；启用任一渠道时必须填写。"
            }
          />
          <div className="grid gap-3">
            <Field className={socialFieldClassName}>
              <FieldLabel htmlFor="social-login-base-url">接口地址</FieldLabel>
              <Input
                id="social-login-base-url"
                value={String(config?.social_login_base_url || "")}
                onChange={(event) => setSocialLoginBaseUrl(event.target.value)}
                placeholder="https://u.zizyw.com"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
            </Field>
            <div className="grid gap-3 md:grid-cols-2">
              <Field className={socialFieldClassName}>
                <FieldLabel htmlFor="social-login-app-id">APPID</FieldLabel>
                <Input
                  id="social-login-app-id"
                  value={String(config?.social_login_app_id || "")}
                  onChange={(event) => setSocialLoginAppId(event.target.value)}
                  placeholder="41"
                  className={`${settingsDialogInputClassName} font-mono text-sm`}
                />
              </Field>
              <Field className={socialFieldClassName}>
                <FieldLabel htmlFor="social-login-app-key">APPKEY</FieldLabel>
                <Input
                  id="social-login-app-key"
                  type="password"
                  value={String(config?.social_login_app_key || "")}
                  onChange={(event) => setSocialLoginAppKey(event.target.value)}
                  placeholder={appKeyConfigured ? "已配置，留空则保留当前密钥" : "聚合登录 APPKEY"}
                  className={`${settingsDialogInputClassName} font-mono text-sm`}
                />
              </Field>
            </div>
          </div>
        </section>

        <section className={socialSectionClassName}>
          <SocialSectionHeading
            title="回调地址"
            tip="后端 OAuth 回调地址填到聚合登录平台；前端完成页用于站内写入本地会话。"
          />
          <div className="grid gap-3">
            <Field className={socialFieldClassName}>
              <FieldLabel htmlFor="social-login-backend-redirect-url">
                后端 OAuth 回调地址
              </FieldLabel>
              <Input
                id="social-login-backend-redirect-url"
                value={String(config?.social_login_redirect_url || "")}
                onChange={(event) => setSocialLoginRedirectUrl(event.target.value)}
                placeholder="https://images.dfmcn.com/auth/social/oauth/callback"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => void handleUseSuggestedRedirectUrl()}
                  disabled={!redirectUrlSuggestion}
                >
                  <Copy data-icon="inline-start" />
                  填入并复制建议地址
                </Button>
                {redirectUrlSuggestion ? (
                  <code className={settingsInlineCodeClassName}>
                    {redirectUrlSuggestion}
                  </code>
                ) : null}
              </div>
            </Field>
            <Field className={socialFieldClassName}>
              <FieldLabel htmlFor="social-login-frontend-redirect-url">
                前端登录完成页
              </FieldLabel>
              <Input
                id="social-login-frontend-redirect-url"
                value={String(config?.social_login_frontend_redirect_url || "")}
                onChange={(event) => setSocialLoginFrontendRedirectUrl(event.target.value)}
                placeholder="/auth/social/callback"
                className={`${settingsDialogInputClassName} font-mono text-sm`}
              />
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  onClick={() => void handleUseSuggestedFrontendRedirectUrl()}
                  disabled={!frontendRedirectUrlSuggestion}
                >
                  <Copy data-icon="inline-start" />
                  填入并复制建议地址
                </Button>
                {frontendRedirectUrlSuggestion ? (
                  <code className={settingsInlineCodeClassName}>
                    {frontendRedirectUrlSuggestion}
                  </code>
                ) : null}
              </div>
            </Field>
          </div>
        </section>

        <section className={socialSectionClassName}>
          <SocialSectionHeading
            title="登录渠道"
            tip="启用后，普通用户登录页会显示对应登录按钮。"
          />
          <div className="grid gap-3 sm:grid-cols-3">
            <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
              <Checkbox
                checked={Boolean(config?.social_login_qq_enabled)}
                onCheckedChange={(value) => setSocialLoginQQEnabled(Boolean(value))}
                aria-label="启用 QQ 登录"
              />
              <span className="min-w-0 leading-5">QQ 登录</span>
            </label>
            <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
              <Checkbox
                checked={Boolean(config?.social_login_wx_enabled)}
                onCheckedChange={(value) => setSocialLoginWXEnabled(Boolean(value))}
                aria-label="启用微信登录"
              />
              <span className="min-w-0 leading-5">微信登录</span>
            </label>
            <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
              <Checkbox
                checked={Boolean(config?.social_login_douyin_enabled)}
                onCheckedChange={(value) => setSocialLoginDouyinEnabled(Boolean(value))}
                aria-label="启用抖音登录"
              />
              <span className="min-w-0 leading-5">抖音登录</span>
            </label>
          </div>
        </section>
      </div>
    </SettingsCard>
  );
}
