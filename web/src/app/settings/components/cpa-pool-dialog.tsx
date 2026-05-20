"use client";

import { Eye, EyeOff, Link2, LoaderCircle, Save, Unplug } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

import { useSettingsStore } from "../store";
import { settingsDialogInputClassName } from "./settings-ui";

export function CPAPoolDialog() {
  const dialogOpen = useSettingsStore((state) => state.dialogOpen);
  const editingPool = useSettingsStore((state) => state.editingPool);
  const formName = useSettingsStore((state) => state.formName);
  const formBaseUrl = useSettingsStore((state) => state.formBaseUrl);
  const formSecretKey = useSettingsStore((state) => state.formSecretKey);
  const showSecret = useSettingsStore((state) => state.showSecret);
  const isSavingPool = useSettingsStore((state) => state.isSavingPool);
  const setDialogOpen = useSettingsStore((state) => state.setDialogOpen);
  const setFormName = useSettingsStore((state) => state.setFormName);
  const setFormBaseUrl = useSettingsStore((state) => state.setFormBaseUrl);
  const setFormSecretKey = useSettingsStore((state) => state.setFormSecretKey);
  const setShowSecret = useSettingsStore((state) => state.setShowSecret);
  const savePool = useSettingsStore((state) => state.savePool);

  return (
    <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
      <DialogContent showCloseButton={false} className="rounded-2xl p-6">
        <DialogHeader className="gap-2">
          <DialogTitle>{editingPool ? "编辑连接" : "添加连接"}</DialogTitle>
          <DialogDescription className="text-sm leading-6">
            {editingPool
              ? "修改 CPA 连接信息"
              : "添加一个新的 CLIProxyAPI 连接"}
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="cpa-pool-name">名称（可选）</FieldLabel>
            <Input
              id="cpa-pool-name"
              value={formName}
              onChange={(event) => setFormName(event.target.value)}
              placeholder="例如：主号池、备用池"
              className={settingsDialogInputClassName}
            />
          </Field>
          <Field>
            <FieldLabel
              htmlFor="cpa-pool-base-url"
              className="flex items-center gap-1.5"
            >
              <Link2 className="size-3.5" />
              CPA 地址
            </FieldLabel>
            <Input
              id="cpa-pool-base-url"
              value={formBaseUrl}
              onChange={(event) => setFormBaseUrl(event.target.value)}
              placeholder="http://your-cpa-host:8317"
              className={settingsDialogInputClassName}
            />
          </Field>
          <Field>
            <FieldLabel
              htmlFor="cpa-pool-secret-key"
              className="flex items-center gap-1.5"
            >
              <Unplug className="size-3.5" />
              Management Secret Key
            </FieldLabel>
            <div className="relative">
              <Input
                id="cpa-pool-secret-key"
                type={showSecret ? "text" : "password"}
                value={formSecretKey}
                onChange={(event) => setFormSecretKey(event.target.value)}
                placeholder={editingPool ? "留空则不修改密钥" : "CPA 管理密钥"}
                className={`${settingsDialogInputClassName} pr-10`}
              />
              <button
                type="button"
                className="absolute top-1/2 right-3 -translate-y-1/2 text-muted-foreground transition hover:text-foreground"
                onClick={() => setShowSecret(!showSecret)}
                aria-label={showSecret ? "隐藏 CPA 管理密钥" : "显示 CPA 管理密钥"}
                title={showSecret ? "隐藏密钥" : "显示密钥"}
              >
                {showSecret ? (
                  <EyeOff className="size-4" />
                ) : (
                  <Eye className="size-4" />
                )}
              </button>
            </div>
          </Field>
        </FieldGroup>
        <DialogFooter className="pt-2">
          <Button
            variant="secondary"
            size="lg"
            onClick={() => setDialogOpen(false)}
            disabled={isSavingPool}
          >
            取消
          </Button>
          <Button
            size="lg"
            onClick={() => void savePool()}
            disabled={isSavingPool}
          >
            {isSavingPool ? (
              <LoaderCircle data-icon="inline-start" className="animate-spin" />
            ) : (
              <Save data-icon="inline-start" />
            )}
            {editingPool ? "保存修改" : "添加"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
