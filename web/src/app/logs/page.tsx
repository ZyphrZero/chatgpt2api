"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { ChevronLeft, ChevronRight, Copy, LoaderCircle, RefreshCw, Search, X } from "lucide-react";
import { toast } from "sonner";

import { DateRangeFilter } from "@/components/date-range-filter";
import { ImageLightbox } from "@/components/image-lightbox";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchSystemLogs, type SystemLog, type SystemLogFilters } from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";

const methodOptions = ["GET", "POST", "PUT", "PATCH", "DELETE"];
const statusOptions = ["200", "201", "400", "401", "403", "404", "422", "429", "500", "502"];
const logLevelOptions = ["info", "warning", "error"];

const emptyFilters: SystemLogFilters = {
  username: "",
  module: "",
  summary: "",
  method: "all",
  status: "all",
  ip_address: "",
  operation_type: "",
  log_level: "all",
  start_date: "",
  end_date: "",
};

const detailLabels: Record<string, string> = {
  endpoint: "接口",
  model: "模型",
  method: "方法",
  path: "路径",
  module: "模块",
  status: "状态",
  outcome: "结果",
  log_level: "日志级别",
  operation_type: "操作类型",
  duration_ms: "耗时",
  response_time: "响应时间",
  started_at: "开始时间",
  ended_at: "结束时间",
  username: "操作人",
  key_name: "令牌名称",
  key_role: "角色",
  key_id: "凭据 ID",
  subject_id: "用户 ID",
  provider: "来源",
  ip_address: "IP 地址",
  user_agent: "User-Agent",
  error: "错误",
  token: "令牌",
  source: "来源事件",
  added: "新增",
  skipped: "跳过",
  removed: "删除",
};

const primaryDetailKeys = [
  "method",
  "path",
  "endpoint",
  "module",
  "operation_type",
  "status",
  "outcome",
  "log_level",
  "duration_ms",
  "username",
  "key_name",
  "subject_id",
  "key_id",
  "ip_address",
  "started_at",
  "ended_at",
];

function primitiveText(value: unknown) {
  return typeof value === "string" || typeof value === "number" ? String(value) : "";
}

function detailValue(item: SystemLog | null, key: string) {
  return item?.detail?.[key];
}

function detailText(item: SystemLog | null, key: string) {
  return primitiveText(detailValue(item, key));
}

function actorText(item: SystemLog | null) {
  return detailText(item, "username") || detailText(item, "key_name") || detailText(item, "subject_id") || detailText(item, "key_id") || "-";
}

function moduleText(item: SystemLog | null) {
  return detailText(item, "module") || "系统日志";
}

function pathText(item: SystemLog | null) {
  return detailText(item, "path") || detailText(item, "endpoint") || "-";
}

function logLevel(item: SystemLog | null) {
  const explicit = detailText(item, "log_level");
  if (explicit) return explicit;
  return detailValue(item, "status") === "failed" ? "warning" : "info";
}

function statusText(item: SystemLog | null) {
  const status = detailValue(item, "status");
  if (status === "success") return "成功";
  if (status === "failed") return "失败";
  if (typeof status === "string" || typeof status === "number") return String(status);
  const outcome = detailValue(item, "outcome");
  if (outcome === "success") return "成功";
  if (outcome === "failed") return "失败";
  return "-";
}

function formatDuration(item: SystemLog | null) {
  const value = detailValue(item, "duration_ms") ?? detailValue(item, "response_time");
  return typeof value === "number" ? `${(value / 1000).toFixed(2)} s` : "-";
}

function statusBadgeVariant(item: SystemLog | null) {
  const status = detailValue(item, "status");
  if (status === "success") return "success";
  if (status === "failed") return "danger";
  const numeric = typeof status === "number" ? status : Number(status);
  if (Number.isFinite(numeric)) {
    if (numeric >= 500) return "danger";
    if (numeric >= 400) return "warning";
    return "success";
  }
  const outcome = detailValue(item, "outcome");
  if (outcome === "success") return "success";
  if (outcome === "failed") return "danger";
  return "secondary";
}

function levelBadgeVariant(level: string) {
  if (level === "error") return "danger";
  if (level === "warning") return "warning";
  return "secondary";
}

function methodBadgeVariant(method: string) {
  if (method === "POST") return "default";
  if (method === "DELETE") return "danger";
  if (method === "PATCH" || method === "PUT") return "warning";
  return "secondary";
}

function getUrls(item: SystemLog | null) {
  const urls = item?.detail?.urls;
  return Array.isArray(urls) ? urls.filter((url): url is string => typeof url === "string") : [];
}

function detailLabel(key: string) {
  return detailLabels[key] || key;
}

function isPrimitiveDetail(value: unknown) {
  return value === null || ["string", "number", "boolean"].includes(typeof value);
}

function formatDetailValue(key: string, value: unknown) {
  if (value === null || value === undefined || value === "") return "—";
  if ((key === "duration_ms" || key === "response_time") && typeof value === "number") return `${(value / 1000).toFixed(2)} s`;
  if (key === "status" || key === "outcome") {
    if (value === "success") return "成功";
    if (value === "failed") return "失败";
  }
  if (typeof value === "boolean") return value ? "是" : "否";
  return String(value);
}

function getPrimaryDetailEntries(item: SystemLog | null) {
  const detail = item?.detail || {};
  return primaryDetailKeys
    .filter((key) => key in detail && isPrimitiveDetail(detail[key]))
    .map((key) => [key, detail[key]] as const);
}

function getExtraDetailEntries(item: SystemLog | null) {
  const detail = item?.detail || {};
  const skipped = new Set([...primaryDetailKeys, "urls", "error"]);
  return Object.entries(detail).filter(([key, value]) => !skipped.has(key) && isPrimitiveDetail(value));
}

function detailJSON(item: SystemLog | null) {
  return JSON.stringify(item?.detail || {}, null, 2);
}

function normalizeFilters(filters: SystemLogFilters): SystemLogFilters {
  return {
    username: filters.username?.trim() || "",
    module: filters.module?.trim() || "",
    summary: filters.summary?.trim() || "",
    method: filters.method || "all",
    status: filters.status || "all",
    ip_address: filters.ip_address?.trim() || "",
    operation_type: filters.operation_type?.trim() || "",
    log_level: filters.log_level || "all",
    start_date: filters.start_date || "",
    end_date: filters.end_date || "",
  };
}

function LogsContent() {
  const [items, setItems] = useState<SystemLog[]>([]);
  const [filters, setFilters] = useState<SystemLogFilters>(emptyFilters);
  const [query, setQuery] = useState<SystemLogFilters>(emptyFilters);
  const [detailLog, setDetailLog] = useState<SystemLog | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [page, setPage] = useState(1);
  const [isLoading, setIsLoading] = useState(true);
  const detailUrls = getUrls(detailLog);
  const detailImages = detailUrls.map((url, index) => ({ id: `${index}`, src: url }));
  const pageSize = 15;
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize));
  const safePage = Math.min(page, pageCount);
  const currentRows = items.slice((safePage - 1) * pageSize, safePage * pageSize);

  const loadLogs = useCallback(async (nextQuery: SystemLogFilters) => {
    setIsLoading(true);
    try {
      const data = await fetchSystemLogs(nextQuery);
      setItems(data.items);
      setPage(1);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载日志失败");
    } finally {
      setIsLoading(false);
    }
  }, []);

  const updateFilter = (key: keyof SystemLogFilters, value: string) => {
    setFilters((current) => ({ ...current, [key]: value }));
  };

  const handleSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setQuery(normalizeFilters(filters));
  };

  const clearFilters = () => {
    setFilters(emptyFilters);
    setQuery(emptyFilters);
  };

  const openDetail = (item: SystemLog) => {
    setDetailLog(item);
    setDetailOpen(true);
  };

  const handleCopyDetailJSON = async () => {
    try {
      await navigator.clipboard.writeText(detailJSON(detailLog));
      toast.success("日志详情已复制");
    } catch {
      toast.error("复制失败，请手动复制");
    }
  };

  useEffect(() => {
    void loadLogs(query);
  }, [loadLogs, query]);

  return (
    <section className="flex flex-col gap-5">
      <PageHeader eyebrow="Logs" title="日志管理" />

      <Card>
        <CardHeader className="pb-4">
          <CardTitle>筛选条件</CardTitle>
        </CardHeader>
        <CardContent>
          <form className="grid gap-3 md:grid-cols-2 xl:grid-cols-4" onSubmit={handleSearch}>
            <Input aria-label="按操作人筛选日志" placeholder="操作人" value={filters.username || ""} onChange={(event) => updateFilter("username", event.target.value)} />
            <Input aria-label="按模块筛选日志" placeholder="模块" value={filters.module || ""} onChange={(event) => updateFilter("module", event.target.value)} />
            <Input aria-label="按摘要或接口筛选日志" placeholder="摘要或接口" value={filters.summary || ""} onChange={(event) => updateFilter("summary", event.target.value)} />
            <Input aria-label="按 IP 地址筛选日志" placeholder="IP 地址" value={filters.ip_address || ""} onChange={(event) => updateFilter("ip_address", event.target.value)} />
            <Input aria-label="按操作类型筛选日志" placeholder="操作类型" value={filters.operation_type || ""} onChange={(event) => updateFilter("operation_type", event.target.value)} />
            <Select value={filters.method || "all"} onValueChange={(value) => updateFilter("method", value)}>
              <SelectTrigger aria-label="按请求方法筛选日志"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部方法</SelectItem>
                {methodOptions.map((method) => <SelectItem key={method} value={method}>{method}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select value={filters.status || "all"} onValueChange={(value) => updateFilter("status", value)}>
              <SelectTrigger aria-label="按状态码筛选日志"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态码</SelectItem>
                {statusOptions.map((status) => <SelectItem key={status} value={status}>{status}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select value={filters.log_level || "all"} onValueChange={(value) => updateFilter("log_level", value)}>
              <SelectTrigger aria-label="按日志级别筛选日志"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部级别</SelectItem>
                {logLevelOptions.map((level) => <SelectItem key={level} value={level}>{level}</SelectItem>)}
              </SelectContent>
            </Select>
            <div className="md:col-span-2 xl:col-span-2">
              <DateRangeFilter
                startDate={filters.start_date || ""}
                endDate={filters.end_date || ""}
                onChange={(startDate, endDate) => {
                  updateFilter("start_date", startDate);
                  updateFilter("end_date", endDate);
                }}
              />
            </div>
            <div className="flex gap-2 md:col-span-2 xl:col-span-2">
              <Button type="submit" disabled={isLoading} className="h-10 rounded-lg">
                {isLoading ? <LoaderCircle className="size-4 animate-spin" /> : <Search className="size-4" />}
                查询
              </Button>
              <Button type="button" variant="outline" onClick={clearFilters} className="h-10 rounded-lg">
                <X className="size-4" />
                清空
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      <Card className="overflow-hidden">
        <CardContent className="p-0">
          <div className="flex items-center justify-between border-b border-border px-5 py-4 text-sm text-muted-foreground">
            <span>共 {items.length} 条</span>
            <Button variant="ghost" className="h-8 rounded-lg px-3" onClick={() => void loadLogs(query)} disabled={isLoading}>
              <RefreshCw className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
              刷新
            </Button>
          </div>
          <div className="overflow-x-auto">
            <Table className="min-w-[1040px]">
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>操作人</TableHead>
                  <TableHead>模块</TableHead>
                  <TableHead>接口</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>耗时</TableHead>
                  <TableHead>摘要</TableHead>
                  <TableHead className="w-28">详情</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {currentRows.map((item, index) => {
                  const method = detailText(item, "method");
                  const level = logLevel(item);
                  return (
                    <TableRow key={`${item.time}-${index}`} className="text-muted-foreground">
                      <TableCell className="whitespace-nowrap">{item.time}</TableCell>
                      <TableCell className="max-w-[150px] truncate text-foreground">{actorText(item)}</TableCell>
                      <TableCell><Badge variant="secondary" className="rounded-md">{moduleText(item)}</Badge></TableCell>
                      <TableCell className="max-w-[260px]">
                        <div className="flex min-w-0 items-center gap-2">
                          {method ? <Badge variant={methodBadgeVariant(method)} className="rounded-md">{method}</Badge> : null}
                          <span className="truncate">{pathText(item)}</span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Badge variant={statusBadgeVariant(item)} className="rounded-md">{statusText(item)}</Badge>
                          <Badge variant={levelBadgeVariant(level)} className="rounded-md">{level}</Badge>
                        </div>
                      </TableCell>
                      <TableCell>{formatDuration(item)}</TableCell>
                      <TableCell className="max-w-[300px] truncate text-muted-foreground">{item.summary || "-"}</TableCell>
                      <TableCell>
                        <Button variant="ghost" className="h-8 rounded-lg px-3" onClick={() => openDetail(item)}>
                          查看详情
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3 text-sm text-muted-foreground">
            <span>第 {safePage} / {pageCount} 页，共 {items.length} 条</span>
            <Button
              variant="outline"
              size="icon"
              className="size-9 rounded-lg"
              disabled={safePage <= 1}
              onClick={() => setPage((value) => Math.max(1, value - 1))}
              aria-label="上一页日志"
              title="上一页"
            >
              <ChevronLeft className="size-4" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="size-9 rounded-lg"
              disabled={safePage >= pageCount}
              onClick={() => setPage((value) => Math.min(pageCount, value + 1))}
              aria-label="下一页日志"
              title="下一页"
            >
              <ChevronRight className="size-4" />
            </Button>
          </div>
          {!isLoading && items.length === 0 ? <div className="px-6 py-14 text-center text-sm text-stone-500">没有找到日志</div> : null}
        </CardContent>
      </Card>

      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="flex max-h-[90vh] w-[min(94vw,980px)] grid-rows-none flex-col gap-0 overflow-hidden rounded-2xl p-0">
          <DialogHeader className="border-b border-border px-6 py-5 pr-12">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0 space-y-2">
                <DialogTitle>日志详情</DialogTitle>
                <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                  <Badge variant="secondary" className="rounded-md">{moduleText(detailLog)}</Badge>
                  <Badge variant={statusBadgeVariant(detailLog)} className="rounded-md">{statusText(detailLog)}</Badge>
                  <Badge variant={levelBadgeVariant(logLevel(detailLog))} className="rounded-md">{logLevel(detailLog)}</Badge>
                  <span>{detailLog?.time || "—"}</span>
                </div>
              </div>
              <Button type="button" variant="outline" className="h-9 rounded-lg px-3" onClick={() => void handleCopyDetailJSON()}>
                <Copy className="size-4" />
                复制 JSON
              </Button>
            </div>
          </DialogHeader>
          <div className="min-h-0 overflow-y-auto px-6 py-5">
            <div className="space-y-5">
              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">摘要</div>
                <div className="rounded-xl border border-border bg-muted/35 p-4">
                  <div className="text-sm font-medium text-foreground">{detailLog?.summary || "—"}</div>
                  <div className="mt-3 grid gap-3 text-sm sm:grid-cols-4">
                    <div>
                      <div className="text-xs text-muted-foreground">操作人</div>
                      <div className="mt-1 truncate font-medium text-foreground">{actorText(detailLog)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">模块</div>
                      <div className="mt-1 truncate font-medium text-foreground">{moduleText(detailLog)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">接口</div>
                      <div className="mt-1 truncate font-medium text-foreground">{pathText(detailLog)}</div>
                    </div>
                    <div>
                      <div className="text-xs text-muted-foreground">耗时</div>
                      <div className="mt-1 font-medium text-foreground">{formatDuration(detailLog)}</div>
                    </div>
                  </div>
                </div>
              </section>

              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">关键字段</div>
                <div className="grid gap-2 md:grid-cols-2">
                  {[...getPrimaryDetailEntries(detailLog), ...getExtraDetailEntries(detailLog)].map(([key, value]) => (
                    <div key={key} className="flex min-w-0 items-start justify-between gap-4 rounded-lg border border-border bg-background px-3 py-2 text-sm">
                      <span className="shrink-0 text-muted-foreground">{detailLabel(key)}</span>
                      <span className="min-w-0 break-words text-right font-medium text-foreground">{formatDetailValue(key, value)}</span>
                    </div>
                  ))}
                  {getPrimaryDetailEntries(detailLog).length === 0 && getExtraDetailEntries(detailLog).length === 0 ? (
                    <div className="rounded-lg border border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground md:col-span-2">
                      没有可展示的字段
                    </div>
                  ) : null}
                </div>
              </section>

              {typeof detailLog?.detail?.error === "string" && detailLog.detail.error ? (
                <section className="space-y-3">
                  <div className="text-sm font-semibold text-foreground">错误信息</div>
                  <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-xl border border-rose-200 bg-rose-50 p-4 text-xs leading-6 text-rose-700 dark:border-rose-800 dark:bg-rose-950/30 dark:text-rose-200">
                    {detailLog.detail.error}
                  </pre>
                </section>
              ) : null}

              {detailUrls.length ? (
                <section className="space-y-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="text-sm font-semibold text-foreground">图片结果</div>
                    <Badge variant="secondary" className="rounded-md">{detailUrls.length} 张</Badge>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-3">
                    {detailUrls.map((url, index) => (
                      <button
                        key={url}
                        type="button"
                        className="group overflow-hidden rounded-xl border border-border bg-muted text-left"
                        onClick={() => {
                          setLightboxIndex(index);
                          setLightboxOpen(true);
                        }}
                        aria-label={`预览日志图片 ${index + 1}`}
                      >
                        <div className="aspect-square overflow-hidden bg-muted">
                          <img src={url} alt={`日志图片 ${index + 1}`} className="h-full w-full object-cover transition duration-200 group-hover:scale-[1.02]" />
                        </div>
                        <div className="truncate border-t border-border px-3 py-2 text-xs text-muted-foreground">{url}</div>
                      </button>
                    ))}
                  </div>
                </section>
              ) : null}

              <section className="space-y-3">
                <div className="text-sm font-semibold text-foreground">完整 JSON</div>
                <pre className="max-h-72 overflow-auto rounded-xl border border-border bg-muted/40 p-4 text-xs leading-6 text-foreground">
                  {detailJSON(detailLog)}
                </pre>
              </section>
            </div>
          </div>
        </DialogContent>
      </Dialog>
      <ImageLightbox
        images={detailImages}
        currentIndex={lightboxIndex}
        open={lightboxOpen}
        onOpenChange={setLightboxOpen}
        onIndexChange={setLightboxIndex}
      />
    </section>
  );
}

export default function LogsPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/logs");
  if (isCheckingAuth || !session) {
    return <div className="flex min-h-[40vh] items-center justify-center"><LoaderCircle className="size-5 animate-spin text-stone-400" /></div>;
  }
  return <LogsContent />;
}
