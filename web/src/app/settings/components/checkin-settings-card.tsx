"use client";

import { useEffect, useRef, useState } from "react";
import { CalendarCheck2, LoaderCircle, RotateCcw, Save } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  fetchAdminCheckinConfig,
  fetchAdminCheckinLogs,
  updateAdminCheckinConfig,
  type AdminCheckinConfig,
  type AdminCheckinLog,
} from "@/lib/api";

import {
  SettingsCard,
  SettingsEmptyState,
  SettingsNotice,
  settingsInputClassName,
  settingsListItemClassName,
  settingsToggleClassName,
} from "./settings-ui";

const defaultRewards = [1, 1, 2, 2, 3, 3, 7];

function normalizeRewards(values: number[]) {
  const rewards = defaultRewards.map((fallback, index) => {
    const value = Number(values[index]);
    return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : fallback;
  });
  return rewards.some((reward) => reward > 0) ? rewards : defaultRewards;
}

function formatLogLine(log: AdminCheckinLog) {
  const owner = String(log.owner_id || log.invite_code || "unknown");
  const shortOwner = owner.length > 18 ? `${owner.slice(0, 8)}...${owner.slice(-6)}` : owner;
  return `${log.date || "-"} · ${shortOwner} · 第 ${Number(log.day || 0)} 天 +${Number(log.reward || 0)} 点`;
}

export function CheckinSettingsCard() {
  const didLoadRef = useRef(false);
  const [config, setConfig] = useState<AdminCheckinConfig | null>(null);
  const [logs, setLogs] = useState<AdminCheckinLog[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);

  const load = async () => {
    setIsLoading(true);
    try {
      const [configData, logsData] = await Promise.all([
        fetchAdminCheckinConfig(),
        fetchAdminCheckinLogs(20),
      ]);
      setConfig({
        enabled: Boolean(configData.enabled),
        rewards: normalizeRewards(configData.rewards || []),
      });
      setLogs(logsData.items || []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载签到设置失败");
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    if (didLoadRef.current) {
      return;
    }
    didLoadRef.current = true;
    void load();
  }, []);

  const updateReward = (index: number, value: string) => {
    setConfig((current) => {
      if (!current) {
        return current;
      }
      const rewards = [...current.rewards];
      rewards[index] = Math.max(0, Number(value) || 0);
      return { ...current, rewards };
    });
  };

  const save = async () => {
    if (!config) {
      return;
    }
    setIsSaving(true);
    try {
      const payload = {
        enabled: Boolean(config.enabled),
        rewards: normalizeRewards(config.rewards),
      };
      const data = await updateAdminCheckinConfig(payload);
      setConfig({
        enabled: Boolean(data.enabled),
        rewards: normalizeRewards(data.rewards || []),
      });
      toast.success("签到设置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存签到设置失败");
    } finally {
      setIsSaving(false);
    }
  };

  if (isLoading) {
    return (
      <SettingsCard
        icon={CalendarCheck2}
        title="签到奖励"
        description="配置每日签到点数。"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard
      icon={CalendarCheck2}
      title="签到奖励"
      description="配置连续 7 天签到点数和最近领取记录。"
      tone="amber"
      meta={
        <Badge variant={config?.enabled ? "success" : "secondary"}>
          {config?.enabled ? "已开启" : "已关闭"}
        </Badge>
      }
      action={
        <Button size="lg" onClick={() => void save()} disabled={isSaving}>
          {isSaving ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <Save data-icon="inline-start" />
          )}
          保存
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        <label className={settingsToggleClassName}>
          <Checkbox
            checked={Boolean(config?.enabled)}
            onCheckedChange={(value) =>
              setConfig((current) =>
                current ? { ...current, enabled: Boolean(value) } : current,
              )
            }
          />
          <span className="min-w-0 text-sm font-semibold">开启每日签到</span>
        </label>

        <section className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {(config?.rewards || defaultRewards).map((reward, index) => (
            <Field key={index} className="gap-1.5">
              <FieldLabel htmlFor={`checkin-reward-${index}`} className="text-xs">
                第 {index + 1} 天
              </FieldLabel>
              <Input
                id={`checkin-reward-${index}`}
                type="number"
                min={0}
                inputMode="numeric"
                value={String(reward)}
                onChange={(event) => updateReward(index, event.target.value)}
                className={settingsInputClassName}
              />
            </Field>
          ))}
        </section>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              setConfig((current) =>
                current ? { ...current, rewards: defaultRewards } : current,
              )
            }
          >
            <RotateCcw data-icon="inline-start" />
            恢复默认 1/1/2/2/3/3/7
          </Button>
          <SettingsNotice className="flex-1">
            全部设置为 0 会自动回退默认值，避免用户签到出现 +0 点。
          </SettingsNotice>
        </div>

        <section className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold text-foreground">最近签到</h3>
            <Button type="button" variant="ghost" size="sm" onClick={() => void load()}>
              刷新
            </Button>
          </div>
          {logs.length > 0 ? (
            <div className="flex max-h-56 flex-col gap-2 overflow-auto pr-1">
              {logs.slice(0, 12).map((log, index) => (
                <div key={`${log.owner_id || log.invite_code || "log"}-${log.date || index}-${index}`} className={settingsListItemClassName}>
                  <p className="text-xs font-medium text-foreground">{formatLogLine(log)}</p>
                </div>
              ))}
            </div>
          ) : (
            <SettingsEmptyState
              icon={CalendarCheck2}
              title="暂无签到记录"
              description="用户完成签到后会显示最近领取记录。"
            />
          )}
        </section>
      </div>
    </SettingsCard>
  );
}
