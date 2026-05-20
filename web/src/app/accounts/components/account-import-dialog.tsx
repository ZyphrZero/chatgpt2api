"use client";

import { useRef, useState, type ChangeEvent } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  ExternalLink,
  FileJson,
  FileText,
  Files,
  KeyRound,
  LoaderCircle,
  ServerCog,
  Upload,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { createAccounts, type Account } from "@/lib/api";
import { cn } from "@/lib/utils";

type ImportMethod = "menu" | "token" | "session" | "cpa";

type AccountImportDialogProps = {
  disabled?: boolean;
  onImported: (items: Account[]) => void;
};

type PendingCpaImport = {
  tokens: string[];
  parsedFileCount: number;
  errorCount: number;
};

const sessionUrl = "https://chatgpt.com/api/auth/session";

function splitTokens(value: string) {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function getSessionAccessToken(value: unknown) {
  const token = (value as { accessToken?: unknown })?.accessToken;
  return typeof token === "string" ? token.trim() : "";
}

function getCpaAccessToken(value: unknown) {
  const token = (value as { access_token?: unknown })?.access_token;
  return typeof token === "string" ? token.trim() : "";
}

function readFileAsText(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => reject(reader.error ?? new Error(`读取文件失败: ${file.name}`));
    reader.readAsText(file);
  });
}

function MethodCard({
  title,
  description,
  icon: Icon,
  onClick,
}: {
  title: string;
  description: string;
  icon: typeof KeyRound;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full rounded-2xl border border-stone-200 bg-white p-0 text-left transition hover:border-stone-300 hover:bg-stone-50"
    >
      <Card className="rounded-2xl border-0 bg-transparent shadow-none">
        <CardContent className="flex items-start gap-4 p-4">
          <div className="rounded-xl bg-stone-100 p-3 text-stone-700">
            <Icon className="size-5" />
          </div>
          <div className="space-y-1">
            <div className="text-sm font-semibold text-stone-900">{title}</div>
            <div className="text-sm leading-6 text-stone-500">{description}</div>
          </div>
        </CardContent>
      </Card>
    </button>
  );
}

export function AccountImportDialog({ disabled, onImported }: AccountImportDialogProps) {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [method, setMethod] = useState<ImportMethod>("menu");
  const [tokenInput, setTokenInput] = useState("");
  const [sessionInput, setSessionInput] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [pendingCpaImport, setPendingCpaImport] = useState<PendingCpaImport | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const txtInputRef = useRef<HTMLInputElement | null>(null);
  const cpaInputRef = useRef<HTMLInputElement | null>(null);

  const resetState = () => {
    setMethod("menu");
    setTokenInput("");
    setSessionInput("");
    setPendingCpaImport(null);
    setConfirmOpen(false);
  };

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetState();
    }
  };

  const submitTokens = async (tokens: string[], successText?: string) => {
    const normalizedTokens = tokens.map((item) => item.trim()).filter(Boolean);

    if (normalizedTokens.length === 0) {
      toast.error("请先提供至少一个可用 Token");
      return;
    }

    setIsSubmitting(true);
    try {
      const data = await createAccounts(normalizedTokens);
      onImported(data.items);
      setOpen(false);
      resetState();

      if ((data.errors?.length ?? 0) > 0) {
        const firstError = data.errors?.[0]?.error;
        toast.error(
          `${successText ?? "导入完成"}，新增 ${data.added ?? 0} 个，已刷新 ${data.refreshed ?? 0} 个，失败 ${data.errors?.length ?? 0} 个${firstError ? `，首个错误：${firstError}` : ""}`,
        );
      } else {
        toast.success(
          `${successText ?? "导入完成"}，新增 ${data.added ?? 0} 个，跳过 ${data.skipped ?? 0} 个重复项，已自动刷新账号信息`,
        );
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "导入账户失败";
      toast.error(message);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleImportTokenText = async () => {
    await submitTokens(splitTokens(tokenInput), "Access Token 导入完成");
  };

  const handleTxtSelected = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";

    if (!file) {
      return;
    }

    try {
      const content = await readFileAsText(file);
      const tokens = splitTokens(content);

      if (tokens.length === 0) {
        toast.error("TXT 文件里没有读取到有效 Token");
        return;
      }

      setTokenInput((prev) => {
        const next = [...splitTokens(prev), ...tokens];
        return next.join("\n");
      });
      toast.success(`已从 ${file.name} 读取 ${tokens.length} 个 Token`);
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取 TXT 文件失败";
      toast.error(message);
    }
  };

  const handleImportSessionJson = async () => {
    if (!sessionInput.trim()) {
      toast.error("请先粘贴完整 Session JSON");
      return;
    }

    try {
      const payload = JSON.parse(sessionInput) as unknown;
      const token = getSessionAccessToken(payload);

      if (!token) {
        toast.error("未从 Session JSON 中提取到 accessToken");
        return;
      }

      await submitTokens([token], "Session JSON 导入完成");
    } catch (error) {
      const message = error instanceof Error ? error.message : "Session JSON 解析失败";
      toast.error(message);
    }
  };

  const handleCpaSelected = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";

    if (files.length === 0) {
      return;
    }

    try {
      const results = await Promise.all(
        files.map(async (file) => {
          const raw = await readFileAsText(file);
          const parsed = JSON.parse(raw) as unknown;
          const token = getCpaAccessToken(parsed);
          return {
            token,
          };
        }),
      );

      const tokens = results.map((item) => item.token).filter((item): item is string => Boolean(item));
      const parsedFileCount = tokens.length;
      const errorCount = results.length - parsedFileCount;

      if (parsedFileCount === 0) {
        toast.error("这些 CPA JSON 文件里没有读取到可用 access_token");
        return;
      }

      setPendingCpaImport({
        tokens,
        parsedFileCount,
        errorCount,
      });
      setConfirmOpen(true);
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取 CPA JSON 文件失败";
      toast.error(message);
    }
  };

  const renderMethodBody = () => {
    if (method === "token") {
      const tokenCount = splitTokens(tokenInput).length;

      return (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <button
              type="button"
              onClick={() => setMethod("menu")}
              className="inline-flex items-center gap-1 text-sm text-stone-500 transition hover:text-stone-800"
            >
              <ArrowLeft className="size-4" />
              返回导入方式
            </button>
            <span className="text-xs text-stone-400">当前识别 {tokenCount} 个 Token</span>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium text-stone-700">Access Token 列表</label>
            <Textarea
              placeholder="每行一个 Access Token..."
              value={tokenInput}
              onChange={(event) => setTokenInput(event.target.value)}
              className="min-h-56 resize-none rounded-xl border-stone-200"
            />
          </div>
          <div className="rounded-2xl border border-dashed border-stone-200 bg-stone-50 p-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="space-y-1">
                <div className="text-sm font-medium text-stone-800">从 TXT 文件导入</div>
                <div className="text-sm leading-6 text-stone-500">支持 `.txt`，文件内容也是一行一个 Token。</div>
              </div>
              <Button
                type="button"
                variant="outline"
                className="rounded-xl border-stone-200 bg-white"
                onClick={() => txtInputRef.current?.click()}
                disabled={isSubmitting}
              >
                <FileText className="size-4" />
                选择 TXT
              </Button>
            </div>
          </div>
          <input
            ref={txtInputRef}
            type="file"
            accept=".txt,text/plain"
            className="hidden"
            onChange={(event) => void handleTxtSelected(event)}
          />
        </div>
      );
    }

    if (method === "session") {
      return (
        <div className="space-y-4">
          <button
            type="button"
            onClick={() => setMethod("menu")}
            className="inline-flex items-center gap-1 text-sm text-stone-500 transition hover:text-stone-800"
          >
            <ArrowLeft className="size-4" />
            返回导入方式
          </button>
          <div className="rounded-2xl border border-stone-200 bg-stone-50 p-4 text-sm leading-6 text-stone-600">
            打开
            {" "}
            <a
              href={sessionUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 font-medium text-stone-900 underline underline-offset-4"
            >
              {sessionUrl}
              <ExternalLink className="size-3.5" />
            </a>
            ，复制页面返回的完整 JSON，系统会自动提取其中的 `accessToken` 导入。
          </div>
          <div className="rounded-2xl border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-900">
            <div className="font-medium">风险提示</div>
            <div>
              不要使用自己的大号，尽量使用不常用的小号进行导入，避免出现封号风险。本项目不承担任何封号风险责任。
            </div>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium text-stone-700">Session JSON</label>
            <Textarea
              placeholder='粘贴完整 JSON，例如包含 "accessToken" 的对象...'
              value={sessionInput}
              onChange={(event) => setSessionInput(event.target.value)}
              className="min-h-56 resize-none rounded-xl border-stone-200 font-mono text-xs"
            />
          </div>
        </div>
      );
    }

    if (method === "cpa") {
      return (
        <div className="space-y-4">
          <button
            type="button"
            onClick={() => setMethod("menu")}
            className="inline-flex items-center gap-1 text-sm text-stone-500 transition hover:text-stone-800"
          >
            <ArrowLeft className="size-4" />
            返回导入方式
          </button>
          <div className="rounded-2xl border border-dashed border-stone-200 bg-stone-50 p-5">
            <div className="space-y-2">
              <div className="text-sm font-medium text-stone-800">多选本地 CPA JSON 文件</div>
              <div className="text-sm leading-6 text-stone-500">
                每个文件应为一个 JSON 对象。系统会从对象中自动提取 `access_token` 或 `accessToken`，
              </div>
            </div>
            <Button
              type="button"
              className="mt-4 rounded-xl bg-stone-950 text-white hover:bg-stone-800"
              onClick={() => cpaInputRef.current?.click()}
              disabled={isSubmitting}
            >
              <Files className="size-4" />
              选择多个 JSON 文件
            </Button>
          </div>
          <input
            ref={cpaInputRef}
            type="file"
            accept=".json,application/json"
            multiple
            className="hidden"
            onChange={(event) => void handleCpaSelected(event)}
          />
          {pendingCpaImport ? (
            <div className="rounded-2xl border border-stone-200 bg-white p-4 text-sm leading-6 text-stone-600">
              最近一次读取到 {pendingCpaImport.parsedFileCount} 个 Token
              {pendingCpaImport.errorCount > 0 ? `，另有 ${pendingCpaImport.errorCount} 个文件未提取成功` : ""}。
            </div>
          ) : null}
        </div>
      );
    }

    return (
      <div className="space-y-3">
        <MethodCard
          title="导入 Access Token"
          description="支持直接粘贴，一行一个；也支持从 TXT 文件读取，一行一个。"
          icon={KeyRound}
          onClick={() => setMethod("token")}
        />
        <MethodCard
          title="导入 Session JSON"
          description="从 chatgpt.com 的 session 接口复制完整 JSON，自动提取 accessToken。"
          icon={FileJson}
          onClick={() => setMethod("session")}
        />
        <MethodCard
          title="导入 CPA JSON 文件"
          description="支持一次多选多个本地 JSON 文件，逐个读取对象里的 access_token 后导入。"
          icon={Files}
          onClick={() => setMethod("cpa")}
        />
        <MethodCard
          title="从远程 CPA 服务器导入"
          description="前往设置页面配置远程 CPA 服务器后再执行导入。"
          icon={Files}
          onClick={() => {
            setOpen(false);
            resetState();
            navigate("/settings");
          }}
        />
        <MethodCard
          title="从 Sub2API 服务器导入"
          description="前往设置页面配置 Sub2API 服务器，再选择其中的 OpenAI 账号导入。"
          icon={ServerCog}
          onClick={() => {
            setOpen(false);
            resetState();
            navigate("/settings");
          }}
        />
      </div>
    );
  };

  const footerDisabled = disabled || isSubmitting;

  return (
    <>
      <Dialog open={open} onOpenChange={handleOpenChange}>
        <Button
          className="h-10 rounded-xl bg-stone-950 px-4 text-white hover:bg-stone-800"
          onClick={() => setOpen(true)}
          disabled={disabled}
        >
          <Upload className="size-4" />
          导入
        </Button>
        <DialogContent showCloseButton={false} className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>
              {method === "menu"
                ? "导入账户"
                : method === "token"
                  ? "导入 Access Token"
                  : method === "session"
                    ? "导入 Session JSON"
                    : "导入 CPA JSON"}
            </DialogTitle>
            <DialogDescription className="text-sm leading-6">
              {method === "menu"
                ? "选择一种导入方式。导入成功后会自动拉取邮箱、类型和额度。"
                : method === "token"
                  ? "支持手动粘贴或从 TXT 文件导入，一行一个 Token。"
                  : method === "session"
                    ? "粘贴完整 Session JSON，系统会自动提取 accessToken。"
                    : "支持一次读取多个本地 JSON 文件，并在提交前做数量确认。"}
            </DialogDescription>
          </DialogHeader>

          {renderMethodBody()}

          <DialogFooter className="pt-2">
            <Button
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setOpen(false)}
              disabled={footerDisabled}
            >
              取消
            </Button>
            {method === "token" ? (
              <Button
                className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
                onClick={() => void handleImportTokenText()}
                disabled={footerDisabled}
              >
                {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
                导入 Token
              </Button>
            ) : null}
            {method === "session" ? (
              <Button
                className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
                onClick={() => void handleImportSessionJson()}
                disabled={footerDisabled}
              >
                {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
                导入 JSON
              </Button>
            ) : null}
            {method === "cpa" ? (
              <Button
                className={cn(
                  "h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800",
                  !pendingCpaImport ? "hidden" : "",
                )}
                onClick={() => setConfirmOpen(true)}
                disabled={footerDisabled || !pendingCpaImport}
              >
                查看导入确认
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>确认导入 CPA Token</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              {pendingCpaImport
                ? `确认识别到 ${pendingCpaImport.parsedFileCount} 个 Token，是否确认导入？`
                : "尚未读取到可导入的 Token。"}
              {pendingCpaImport?.errorCount
                ? `，另有 ${pendingCpaImport.errorCount} 个文件未提取成功。`
                : "。"}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="pt-2">
            <Button
              variant="secondary"
              className="h-10 rounded-xl bg-stone-100 px-5 text-stone-700 hover:bg-stone-200"
              onClick={() => setConfirmOpen(false)}
              disabled={isSubmitting}
            >
              返回
            </Button>
            <Button
              className="h-10 rounded-xl bg-stone-950 px-5 text-white hover:bg-stone-800"
              onClick={() => void submitTokens(pendingCpaImport?.tokens ?? [], "CPA JSON 导入完成")}
              disabled={isSubmitting || !pendingCpaImport}
            >
              {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : null}
              确认导入
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
