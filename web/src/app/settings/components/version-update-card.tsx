"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CheckCircle2,
  Download,
  PackageCheck,
  RefreshCw,
  RotateCcw,
  RotateCw,
  Save,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import webConfig from "@/constants/common-env";
import {
  checkSystemUpdates,
  performSystemUpdate,
  restartSystemService,
  rollbackSystemUpdate,
  type SystemUpdateInfo,
} from "@/lib/api";
import { cn } from "@/lib/utils";

import { useSettingsStore } from "../store";
import { SettingsCard, settingsListItemClassName } from "./settings-ui";

type OperationState = "idle" | "checking" | "updating" | "restarting" | "rolling-back";

function versionLabel(version: string | undefined) {
  const normalized = String(version || "").trim();
  if (!normalized) {
    return "未知版本";
  }
  return normalized.startsWith("v") ? normalized : `v${normalized}`;
}

function releaseDateLabel(value: string | undefined) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  if (date.getFullYear() < 2000) {
    return "";
  }
  return date.toLocaleString();
}

async function waitForServiceReady() {
  for (let attempt = 0; attempt < 8; attempt += 1) {
    try {
      const response = await fetch("/health", { cache: "no-cache" });
      if (response.ok) {
        window.location.reload();
        return;
      }
    } catch {
      // Service is expected to be briefly unavailable while restarting.
    }
    await new Promise((resolve) => window.setTimeout(resolve, 1000));
  }
  window.location.reload();
}

export function VersionUpdateCard({
  canManageSystem,
}: {
  canManageSystem: boolean;
}) {
  const [updateInfo, setUpdateInfo] = useState<SystemUpdateInfo | null>(null);
  const [operation, setOperation] = useState<OperationState>("idle");
  const [needsRestart, setNeedsRestart] = useState(false);
  const [lastError, setLastError] = useState("");
  const config = useSettingsStore((state) => state.config);
  const isSavingConfig = useSettingsStore((state) => state.isSavingConfig);
  const setUpdateRepo = useSettingsStore((state) => state.setUpdateRepo);
  const setUpdateGitHubToken = useSettingsStore(
    (state) => state.setUpdateGitHubToken,
  );
  const saveConfig = useSettingsStore((state) => state.saveConfig);

  const currentVersion = updateInfo?.current_version || webConfig.appVersion;
  const latestVersion = updateInfo?.latest_version || "";
  const releaseDate = useMemo(
    () => releaseDateLabel(updateInfo?.release_info?.published_at),
    [updateInfo?.release_info?.published_at],
  );
  const isReleaseBuild = updateInfo?.build_type === "release";
  const hasUpdate = Boolean(updateInfo?.has_update);
  const hasWarning = Boolean(updateInfo?.warning);
  const isBusy = operation !== "idle";
  const updateRepo =
    typeof config?.update_repo === "string"
      ? config.update_repo
      : "ZyphrZero/chatgpt2api";
  const updateGitHubToken = String(config?.update_github_token || "");
  const updateGitHubTokenConfigured = Boolean(
    config?.update_github_token_configured,
  );

  const refreshUpdates = useCallback(async (force = false) => {
    if (!canManageSystem) {
      return;
    }
    setOperation("checking");
    setLastError("");
    try {
      const data = await checkSystemUpdates(force);
      setUpdateInfo(data);
      if (force) {
        if (data.warning) {
          toast.warning(data.warning);
        } else {
          toast.success(data.has_update ? "发现新版本" : "当前已是最新版本");
        }
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "检查更新失败";
      setLastError(message);
      toast.error(message);
    } finally {
      setOperation("idle");
    }
  }, [canManageSystem]);

  async function handleUpdate() {
    if (!canManageSystem) {
      return;
    }
    setOperation("updating");
    setLastError("");
    try {
      const result = await performSystemUpdate();
      setNeedsRestart(result.need_restart);
      toast.success("更新已完成，请重启服务");
    } catch (error) {
      const message = error instanceof Error ? error.message : "更新失败";
      setLastError(message);
      toast.error(message);
    } finally {
      setOperation("idle");
    }
  }

  async function handleRollback() {
    if (!canManageSystem) {
      return;
    }
    setOperation("rolling-back");
    setLastError("");
    try {
      const result = await rollbackSystemUpdate();
      setNeedsRestart(result.need_restart);
      toast.success("回滚已完成，请重启服务");
    } catch (error) {
      const message = error instanceof Error ? error.message : "回滚失败";
      setLastError(message);
      toast.error(message);
    } finally {
      setOperation("idle");
    }
  }

  async function handleRestart() {
    if (!canManageSystem) {
      return;
    }
    setOperation("restarting");
    setLastError("");
    try {
      await restartSystemService();
    } catch {
      // The restart request may drop the connection after the backend exits.
    }
    toast.info("服务正在重启");
    await waitForServiceReady();
  }

  useEffect(() => {
    if (canManageSystem) {
      void refreshUpdates(false);
    }
  }, [canManageSystem, refreshUpdates]);

  return (
    <SettingsCard
      icon={PackageCheck}
      title="版本更新"
      description="查看当前部署版本。"
      tone={hasWarning || hasUpdate ? "amber" : "slate"}
      meta={
        updateInfo ? (
          <Badge variant={hasWarning || hasUpdate ? "warning" : "success"}>
            {hasWarning ? "检查失败" : hasUpdate ? "有可用更新" : "已是最新"}
          </Badge>
        ) : null
      }
      action={
        <Button
          size="lg"
          variant="outline"
          disabled={isBusy || !canManageSystem}
          onClick={() => void refreshUpdates(true)}
        >
          <RefreshCw
            data-icon="inline-start"
            className={cn(operation === "checking" && "animate-spin")}
          />
          检查更新
        </Button>
      }
    >
      <div className="space-y-3">
        <div className={settingsListItemClassName}>
          <div className="grid gap-3 sm:grid-cols-2">
            <VersionField label="当前版本" value={versionLabel(currentVersion)} />
            <VersionField
              label="最新版本"
              value={latestVersion ? versionLabel(latestVersion) : "等待检查"}
            />
            <VersionField
              label="构建类型"
              value={isReleaseBuild ? "release" : updateInfo?.build_type || "source"}
            />
            <VersionField label="发布时间" value={releaseDate || "暂无"} />
          </div>
        </div>

        {updateInfo?.warning ? (
          <StatusPanel tone="warning">{updateInfo.warning}</StatusPanel>
        ) : null}
        {lastError ? <StatusPanel tone="danger">{lastError}</StatusPanel> : null}
        {needsRestart ? (
          <StatusPanel tone="success">更新文件已替换，重启后生效。</StatusPanel>
        ) : null}
        {hasUpdate && !isReleaseBuild ? (
          <StatusPanel tone="warning">
            当前是源码构建，请使用 Git 或 Docker Compose 的部署流程更新。
          </StatusPanel>
        ) : null}
        {!canManageSystem ? (
          <StatusPanel tone="warning">只有管理员可以检查和执行系统更新。</StatusPanel>
        ) : null}

        <div className={settingsListItemClassName}>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p className="text-sm font-semibold text-foreground">
                  GitHub Release 源
                </p>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">
                  检查该仓库的 latest release；公开 Release 不需要 Token，403
                  通常是匿名 API 额度耗尽。
                </p>
              </div>
              <Badge
                variant={updateGitHubTokenConfigured ? "success" : "secondary"}
              >
                {updateGitHubTokenConfigured ? "Token 已配置" : "Token 可选"}
              </Badge>
            </div>
            <div className="grid gap-2">
              <Input
                value={updateRepo}
                onChange={(event) => setUpdateRepo(event.target.value)}
                placeholder="owner/repo"
                disabled={!canManageSystem || isSavingConfig}
                className="font-mono text-sm"
              />
              <Input
                type="password"
                value={updateGitHubToken}
                onChange={(event) => setUpdateGitHubToken(event.target.value)}
                placeholder={
                  updateGitHubTokenConfigured
                    ? "已配置，留空则保留当前 Token"
                    : "可选：GitHub API Token"
                }
                disabled={!canManageSystem || isSavingConfig}
                className="font-mono text-sm"
              />
            </div>
            <div>
              <Button
                type="button"
                variant="outline"
                disabled={
                  !canManageSystem ||
                  isSavingConfig ||
                  !updateRepo.trim()
                }
                onClick={() => void saveConfig()}
              >
                {isSavingConfig ? (
                  <RefreshCw data-icon="inline-start" className="animate-spin" />
                ) : (
                  <Save data-icon="inline-start" />
                )}
                保存更新源
              </Button>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap gap-2">
          {canManageSystem && hasUpdate && isReleaseBuild && !needsRestart ? (
            <Button disabled={isBusy} onClick={() => void handleUpdate()}>
              <Download data-icon="inline-start" />
              {operation === "updating" ? "更新中..." : "立即更新"}
            </Button>
          ) : null}
          {canManageSystem && needsRestart ? (
            <Button disabled={isBusy} onClick={() => void handleRestart()}>
              <RotateCw
                data-icon="inline-start"
                className={cn(operation === "restarting" && "animate-spin")}
              />
              重启服务
            </Button>
          ) : null}
          {canManageSystem ? (
            <Button
              variant="outline"
              disabled={isBusy}
              onClick={() => void handleRollback()}
            >
              <RotateCcw data-icon="inline-start" />
              回滚
            </Button>
          ) : null}
          {updateInfo?.release_info?.html_url ? (
            <Button variant="ghost" asChild>
              <a
                href={updateInfo.release_info.html_url}
                target="_blank"
                rel="noopener noreferrer"
              >
                <CheckCircle2 data-icon="inline-start" />
                Release
              </a>
            </Button>
          ) : null}
        </div>
      </div>
    </SettingsCard>
  );
}

function VersionField({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="mt-1 truncate font-mono text-sm font-semibold text-foreground">
        {value}
      </p>
    </div>
  );
}

function StatusPanel({
  children,
  tone,
}: {
  children: string;
  tone: "danger" | "success" | "warning";
}) {
  const className =
    tone === "danger"
      ? "border-rose-200 bg-rose-50 text-rose-700"
      : tone === "success"
        ? "border-emerald-200 bg-emerald-50 text-emerald-700"
        : "border-amber-200 bg-amber-50 text-amber-700";
  return (
    <div className={cn("rounded-[16px] border px-4 py-3 text-sm", className)}>
      {children}
    </div>
  );
}
