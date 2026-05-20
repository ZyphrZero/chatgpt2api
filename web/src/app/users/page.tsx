"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Ban,
  CheckCircle2,
  KeyRound,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
  UserRound,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
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
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  createManagedUser,
  deleteManagedUser,
  fetchManagedRoles,
  fetchManagedUsers,
  updateManagedUser,
  type CreateManagedUserPayload,
  type ManagedUser,
  type ManagedRole,
  type UpdateManagedUserPayload,
} from "@/lib/api";
import { useAuthGuard } from "@/lib/use-auth-guard";

function formatDateTime(value?: string | null) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function normalizeManagedUsers(items: ManagedUser[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

function normalizeManagedRoles(items: ManagedRole[] | null | undefined) {
  return Array.isArray(items) ? items : [];
}

type CreateUserForm = {
  username: string;
  name: string;
  password: string;
  confirmPassword: string;
  role_id: string;
  enabled: boolean;
};

type CreateUserErrors = Partial<Record<"username" | "password" | "confirmPassword", string>>;

type EditUserForm = {
  name: string;
  password: string;
  quotaTotal: string;
  quotaDelta: string;
};

type EditUserErrors = Partial<Record<"password" | "quotaTotal" | "quotaDelta", string>>;

const accountUsernamePattern = /^[a-z0-9][a-z0-9_.-]{2,31}$/;

function createEmptyUserForm(roleId = ""): CreateUserForm {
  return {
    username: "",
    name: "",
    password: "",
    confirmPassword: "",
    role_id: roleId,
    enabled: true,
  };
}

function validateCreateUserForm(values: CreateUserForm) {
  const errors: CreateUserErrors = {};
  const username = values.username.trim().toLowerCase();

  if (!accountUsernamePattern.test(username)) {
    errors.username = "用户名需为 3-32 位小写字母、数字、点、下划线或短横线，并以字母或数字开头";
  }
  if (values.password.length < 8) {
    errors.password = "密码长度不能少于 8 位";
  } else if (values.password.length > 128) {
    errors.password = "密码长度不能超过 128 位";
  }
  if (!values.confirmPassword) {
    errors.confirmPassword = "请确认密码";
  } else if (values.confirmPassword !== values.password) {
    errors.confirmPassword = "两次输入的密码不一致";
  }

  return errors;
}

function createUserPayload(values: CreateUserForm): CreateManagedUserPayload {
  return {
    username: values.username.trim().toLowerCase(),
    name: values.name.trim(),
    password: values.password,
    role_id: values.role_id,
    enabled: values.enabled,
  };
}

function createEditUserForm(user: ManagedUser): EditUserForm {
  return {
    name: user.name || "",
    password: "",
    quotaTotal: user.image_quota_total === null || user.image_quota_total === undefined ? "" : String(user.image_quota_total),
    quotaDelta: "",
  };
}

function validateQuotaInput(value: string, allowNegative = false) {
  const text = value.trim();
  if (text === "") {
    return "";
  }
  if (!/^-?\d+$/.test(text)) {
    return "请输入整数";
  }
  const number = Number.parseInt(text, 10);
  if (!Number.isSafeInteger(number)) {
    return "数值过大";
  }
  if (!allowNegative && number < 0) {
    return "不能小于 0";
  }
  return "";
}

function validateEditUserForm(values: EditUserForm) {
  const errors: EditUserErrors = {};
  if (values.password && values.password.length < 8) {
    errors.password = "密码长度不能少于 8 位";
  } else if (values.password.length > 128) {
    errors.password = "密码长度不能超过 128 位";
  }
  const quotaTotalError = validateQuotaInput(values.quotaTotal);
  if (quotaTotalError) {
    errors.quotaTotal = quotaTotalError;
  }
  const quotaDeltaError = validateQuotaInput(values.quotaDelta, true);
  if (quotaDeltaError) {
    errors.quotaDelta = quotaDeltaError;
  }
  return errors;
}

function providerLabel(provider?: string) {
  if (provider === "linuxdo") {
    return "Linuxdo";
  }
  if (provider === "local") {
    return "本地";
  }
  return provider || "未知";
}

const linuxDoLevelColors: Record<string, string> = {
  "0": "text-stone-500 dark:text-stone-400",
  "1": "text-emerald-600 dark:text-emerald-400",
  "2": "text-blue-600 dark:text-blue-400",
  "3": "text-amber-600 dark:text-amber-400",
};

function numeric(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

const compactNumberFormatter = new Intl.NumberFormat("zh-CN", {
  maximumFractionDigits: 1,
  notation: "compact",
});

const usageDateFormatter = new Intl.DateTimeFormat("zh-CN", {
  day: "2-digit",
  month: "2-digit",
});

type NormalizedUsagePoint = {
  date: string;
  calls: number;
  success: number;
  failure: number;
  quotaUsed: number;
};

function formatCompactNumber(value: unknown) {
  return compactNumberFormatter.format(numeric(value));
}

function formatQuotaLimit(value: unknown) {
  return value === null || value === undefined ? "不限" : numeric(value);
}

function formatUsageDate(value?: string) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return usageDateFormatter.format(date);
}

function normalizeUsageCurve(points?: ManagedUser["usage_curve"]): NormalizedUsagePoint[] {
  if (!Array.isArray(points)) {
    return [];
  }
  return points
    .filter((point) => Boolean(point.date))
    .map((point) => ({
      date: point.date,
      calls: numeric(point.calls),
      success: numeric(point.success),
      failure: numeric(point.failure),
      quotaUsed: numeric(point.quota_used),
    }))
    .sort((left, right) => left.date.localeCompare(right.date));
}

function latestUsagePoint(points?: ManagedUser["usage_curve"]) {
  const safePoints = normalizeUsageCurve(points);
  return safePoints[safePoints.length - 1];
}

function todayQuotaUsed(user: ManagedUser) {
  return latestUsagePoint(user.usage_curve)?.quotaUsed ?? 0;
}

function todayCallCount(user: ManagedUser) {
  return latestUsagePoint(user.usage_curve)?.calls ?? 0;
}

function UsageSparkline({ points }: { points?: ManagedUser["usage_curve"] }) {
  const safePoints = useMemo(() => normalizeUsageCurve(points), [points]);

  if (safePoints.length === 0) {
    return (
      <div
        className="flex h-16 w-[230px] items-center justify-center rounded-lg border border-dashed border-border bg-muted/20 text-xs text-muted-foreground"
        aria-label="调用曲线暂无数据"
      >
        暂无调用
      </div>
    );
  }

  const width = 220;
  const height = 64;
  const paddingX = 8;
  const paddingTop = 8;
  const paddingBottom = 12;
  const baselineY = height - paddingBottom;
  const plotWidth = width - paddingX * 2;
  const plotHeight = height - paddingTop - paddingBottom;
  const calls = safePoints.map((point) => point.calls);
  const maxCalls = Math.max(...calls);
  const minCalls = Math.min(...calls);
  const hasVariation = maxCalls > minCalls;
  const valueRange = Math.max(1, maxCalls - minCalls);
  const chartPoints = safePoints.map((point, index) => {
    const x = safePoints.length === 1 ? width / 2 : paddingX + (index / (safePoints.length - 1)) * plotWidth;
    const y = hasVariation
      ? paddingTop + ((maxCalls - point.calls) / valueRange) * plotHeight
      : maxCalls === 0
        ? baselineY
        : paddingTop + plotHeight / 2;
    return { point, x, y };
  });
  const firstPoint = chartPoints[0];
  const lastPoint = chartPoints[chartPoints.length - 1];
  const linePath = chartPoints.length === 1
    ? `M ${firstPoint.x - 12} ${firstPoint.y} L ${firstPoint.x + 12} ${firstPoint.y}`
    : chartPoints.map(({ x, y }, index) => `${index === 0 ? "M" : "L"} ${x} ${y}`).join(" ");
  const areaPath = chartPoints.length === 1
    ? `M ${firstPoint.x - 12} ${baselineY} L ${firstPoint.x - 12} ${firstPoint.y} L ${firstPoint.x + 12} ${firstPoint.y} L ${firstPoint.x + 12} ${baselineY} Z`
    : `${linePath} L ${lastPoint.x} ${baselineY} L ${firstPoint.x} ${baselineY} Z`;
  const peakPoint = safePoints.reduce((peak, point) => (point.calls > peak.calls ? point : peak), safePoints[0]);
  const latestPoint = lastPoint.point;
  const label = `近 ${safePoints.length} 日调用曲线，今日 ${latestPoint.calls} 次，峰值 ${peakPoint.calls} 次`;

  return (
    <div className="w-[230px] space-y-1.5" aria-label={label}>
      <div className="h-16 overflow-hidden rounded-lg border border-border/70 bg-background">
        <svg viewBox={`0 0 ${width} ${height}`} role="img" className="h-full w-full">
          <title>{label}</title>
          <line x1={paddingX} x2={width - paddingX} y1={paddingTop} y2={paddingTop} className="stroke-border/70" strokeDasharray="3 5" />
          <line x1={paddingX} x2={width - paddingX} y1={baselineY} y2={baselineY} className="stroke-border/70" />
          <path d={areaPath} className="fill-[#3b82f6] opacity-10 dark:opacity-15" />
          <path d={linePath} fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" className="text-[#1456f0] dark:text-sky-300" />
          {chartPoints.map(({ point, x, y }, index) => {
            const isLatest = index === chartPoints.length - 1;
            return (
              <circle
                key={point.date}
                cx={x}
                cy={y}
                r={isLatest ? 3.4 : 2.4}
                className={isLatest ? "fill-[#1456f0] dark:fill-sky-300" : "fill-background stroke-[#1456f0] dark:stroke-sky-300"}
                strokeWidth={isLatest ? 0 : 1.6}
              >
                <title>{`${point.date} 调用 ${point.calls} 次，成功 ${point.success} 次，失败 ${point.failure} 次，额度 ${point.quotaUsed}`}</title>
              </circle>
            );
          })}
        </svg>
      </div>
      <div className="flex items-center justify-between gap-2 text-[11px] leading-4 text-muted-foreground">
        <span>{formatUsageDate(safePoints[0].date)}-{formatUsageDate(latestPoint.date)}</span>
        <span>峰值 {formatCompactNumber(peakPoint.calls)}</span>
      </div>
    </div>
  );
}

function userSearchText(user: ManagedUser) {
  return [
    user.id,
    user.username,
    user.name,
    user.role_id,
    user.role_name,
    user.owner_id,
    user.owner_name,
    user.provider,
    user.linuxdo_level,
    user.session_id,
    user.session_name,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function roleLabel(user: ManagedUser, roles: ManagedRole[]) {
  const roleID = String(user.role_id || "").trim();
  const role = roles.find((item) => item.id === roleID);
  return user.role_name || role?.name || "普通用户";
}

function UsersContent() {
  const [items, setItems] = useState<ManagedUser[]>([]);
  const [roles, setRoles] = useState<ManagedRole[]>([]);
  const [searchText, setSearchText] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [isLoading, setIsLoading] = useState(true);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
  const [createForm, setCreateForm] = useState<CreateUserForm>(() => createEmptyUserForm());
  const [createErrors, setCreateErrors] = useState<CreateUserErrors>({});
  const [isCreating, setIsCreating] = useState(false);
  const [deletingUser, setDeletingUser] = useState<ManagedUser | null>(null);
  const [roleUser, setRoleUser] = useState<ManagedUser | null>(null);
  const [selectedRoleId, setSelectedRoleId] = useState("");
  const [isSavingRole, setIsSavingRole] = useState(false);
  const [editingUser, setEditingUser] = useState<ManagedUser | null>(null);
  const [editForm, setEditForm] = useState<EditUserForm>({ name: "", password: "", quotaTotal: "", quotaDelta: "" });
  const [editErrors, setEditErrors] = useState<EditUserErrors>({});
  const [isSavingEdit, setIsSavingEdit] = useState(false);

  const loadUsers = useCallback(async () => {
    setIsLoading(true);
    try {
      const [firstPage, rolesData] = await Promise.all([
        fetchManagedUsers({ page: 1, page_size: 100 }),
        fetchManagedRoles(),
      ]);
      const nextRoles = normalizeManagedRoles(rolesData.items);
      let aggregated = firstPage.items;
      if (firstPage.total_pages > 1) {
        const extraPages = await Promise.all(
          Array.from({ length: firstPage.total_pages - 1 }, (_, index) =>
            fetchManagedUsers({ page: index + 2, page_size: 100 }),
          ),
        );
        aggregated = aggregated.concat(...extraPages.map((page) => page.items));
      }
      setItems(normalizeManagedUsers(aggregated));
      setRoles(nextRoles);
      setCreateForm((current) => ({
        ...current,
        role_id: current.role_id || nextRoles[0]?.id || "",
      }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载用户失败");
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const filteredItems = useMemo(() => {
    const keyword = searchText.trim().toLowerCase();
    return items.filter((user) => {
      if (providerFilter !== "all" && user.provider !== providerFilter) {
        return false;
      }
      if (statusFilter === "enabled" && !user.enabled) {
        return false;
      }
      if (statusFilter === "disabled" && user.enabled) {
        return false;
      }
      return !keyword || userSearchText(user).includes(keyword);
    });
  }, [items, providerFilter, searchText, statusFilter]);
  const hasActiveFilters = searchText.trim() !== "" || providerFilter !== "all" || statusFilter !== "all";

  const setItemPending = (id: string, isPending: boolean) => {
    setPendingIds((current) => {
      const next = new Set(current);
      if (isPending) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  };

  const updateCreateField = <Key extends keyof CreateUserForm>(field: Key, value: CreateUserForm[Key]) => {
    setCreateForm((current) => ({ ...current, [field]: value }));
    if (field === "username" || field === "password" || field === "confirmPassword") {
      setCreateErrors((current) => ({ ...current, [field]: undefined }));
    }
  };

  const openCreateDialog = () => {
    const roleId = createForm.role_id || roles[0]?.id || "";
    setCreateForm(createEmptyUserForm(roleId));
    setCreateErrors({});
    setIsCreateDialogOpen(true);
  };

  const closeCreateDialog = (open: boolean) => {
    setIsCreateDialogOpen(open);
    if (!open) {
      setCreateErrors({});
      setCreateForm(createEmptyUserForm(createForm.role_id || roles[0]?.id || ""));
    }
  };

  const handleCreate = async () => {
    const nextErrors = validateCreateUserForm(createForm);
    if (Object.keys(nextErrors).length > 0) {
      setCreateErrors(nextErrors);
      return;
    }

    setIsCreating(true);
    try {
      const data = await createManagedUser(createUserPayload(createForm));
      setItems(normalizeManagedUsers(data.items));
      setCreateForm(createEmptyUserForm(createForm.role_id));
      setCreateErrors({});
      closeCreateDialog(false);
      toast.success("用户已创建");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建用户失败");
    } finally {
      setIsCreating(false);
    }
  };

  const handleToggle = async (user: ManagedUser) => {
    setItemPending(user.id, true);
    try {
      const data = await updateManagedUser(user.id, { enabled: !user.enabled });
      setItems(normalizeManagedUsers(data.items));
      toast.success(user.enabled ? "用户已禁用" : "用户已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新用户失败");
    } finally {
      setItemPending(user.id, false);
    }
  };

  const openRoleDialog = (user: ManagedUser) => {
    setRoleUser(user);
    setSelectedRoleId(user.role_id || roles[0]?.id || "");
  };

  const openEditDialog = (user: ManagedUser) => {
    setEditingUser(user);
    setEditForm(createEditUserForm(user));
    setEditErrors({});
  };

  const updateEditField = <Key extends keyof EditUserForm>(field: Key, value: EditUserForm[Key]) => {
    setEditForm((current) => ({ ...current, [field]: value }));
    setEditErrors((current) => ({ ...current, [field]: undefined }));
  };

  const handleSaveEdit = async () => {
    if (!editingUser) {
      return;
    }
    const nextErrors = validateEditUserForm(editForm);
    if (Object.keys(nextErrors).length > 0) {
      setEditErrors(nextErrors);
      return;
    }

    const updates: UpdateManagedUserPayload = {};
    if (editForm.name.trim() !== (editingUser.name || "")) {
      updates.name = editForm.name.trim();
    }
    if (editForm.password) {
      updates.password = editForm.password;
    }
    const originalQuotaTotal = createEditUserForm(editingUser).quotaTotal;
    if (editForm.quotaTotal.trim() !== "" && editForm.quotaTotal.trim() !== originalQuotaTotal) {
      updates.image_quota_total = Number.parseInt(editForm.quotaTotal.trim(), 10);
    }
    if (editForm.quotaDelta.trim() !== "") {
      updates.image_quota_delta = Number.parseInt(editForm.quotaDelta.trim(), 10);
    }
    if (Object.keys(updates).length === 0) {
      toast.message("没有需要保存的改动");
      return;
    }

    const user = editingUser;
    setIsSavingEdit(true);
    setItemPending(user.id, true);
    try {
      const data = await updateManagedUser(user.id, updates);
      setItems(normalizeManagedUsers(data.items));
      setEditingUser(null);
      setEditErrors({});
      toast.success("用户已更新");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存用户失败");
    } finally {
      setIsSavingEdit(false);
      setItemPending(user.id, false);
    }
  };

  const handleSaveRole = async () => {
    if (!roleUser || !selectedRoleId) {
      return;
    }
    const user = roleUser;
    setIsSavingRole(true);
    setItemPending(user.id, true);
    try {
      const data = await updateManagedUser(user.id, {
        role_id: selectedRoleId,
      });
      setItems(normalizeManagedUsers(data.items));
      setRoleUser(null);
      toast.success("角色已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存角色失败");
    } finally {
      setIsSavingRole(false);
      setItemPending(user.id, false);
    }
  };

  const handleDelete = async () => {
    if (!deletingUser) {
      return;
    }
    const user = deletingUser;
    setItemPending(user.id, true);
    try {
      const data = await deleteManagedUser(user.id);
      setItems(normalizeManagedUsers(data.items));
      setDeletingUser(null);
      toast.success("用户已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除用户失败");
    } finally {
      setItemPending(user.id, false);
    }
  };

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        eyebrow="Users"
        title="用户管理"
        actions={
          <>
            <Button variant="outline" onClick={() => void loadUsers()} disabled={isLoading} className="h-10 rounded-lg">
              <RefreshCw className={`size-4 ${isLoading ? "animate-spin" : ""}`} />
              刷新
            </Button>
            <Button onClick={openCreateDialog} className="h-10 rounded-lg">
              <Plus className="size-4" />
              创建用户
            </Button>
          </>
        }
      />

      <Card className="overflow-hidden">
        <CardContent className="p-0">
          <div className="flex flex-col gap-3 border-b border-border px-5 py-4">
            <div className="flex items-center justify-between text-sm text-muted-foreground">
              <span>共 {filteredItems.length} / {items.length} 个用户</span>
            </div>
            <div className="grid gap-2 lg:grid-cols-[minmax(18rem,1fr)_160px_160px_auto]">
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={searchText}
                  onChange={(event) => setSearchText(event.target.value)}
                  placeholder="搜索用户名、用户 ID、owner 或会话"
                  className="h-10 rounded-lg pl-9"
                />
              </div>
              <Select value={providerFilter} onValueChange={setProviderFilter}>
                <SelectTrigger className="h-10 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部来源</SelectItem>
                  <SelectItem value="linuxdo">Linuxdo</SelectItem>
                  <SelectItem value="local">本地</SelectItem>
                </SelectContent>
              </Select>
              <Select value={statusFilter} onValueChange={setStatusFilter}>
                <SelectTrigger className="h-10 rounded-lg">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部状态</SelectItem>
                  <SelectItem value="enabled">已启用</SelectItem>
                  <SelectItem value="disabled">已禁用</SelectItem>
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="outline"
                className="h-10 rounded-lg px-3"
                disabled={!hasActiveFilters}
                onClick={() => {
                  setSearchText("");
                  setProviderFilter("all");
                  setStatusFilter("all");
                }}
              >
                <X className="size-4" />
                清除
              </Button>
            </div>
          </div>
          <div className="overflow-x-auto">
            <Table className="min-w-[1340px]">
              <TableHeader>
                <TableRow>
                  <TableHead>用户</TableHead>
                  <TableHead>角色</TableHead>
                  <TableHead>来源</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>额度消耗</TableHead>
                  <TableHead className="w-[340px]">调用曲线</TableHead>
                  <TableHead>时间</TableHead>
                  <TableHead className="w-[340px]">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredItems.map((user) => {
                  const isPending = pendingIds.has(user.id);
                  return (
                    <TableRow key={user.id} className="text-muted-foreground">
                      <TableCell>
                        <div className="min-w-0 space-y-1">
                          <div className="truncate font-medium text-foreground">{user.name || "普通用户"}</div>
                          {user.username ? (
                            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                              <UserRound className="size-3.5" />
                              <span className="truncate">{user.username}</span>
                            </div>
                          ) : null}
                          <code className="block max-w-[260px] truncate font-mono text-xs text-muted-foreground">{user.id}</code>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col items-start gap-1">
                          <Badge variant="secondary" className="rounded-md">
                            {roleLabel(user, roles)}
                          </Badge>
                          <code className="max-w-[160px] truncate font-mono text-xs text-muted-foreground">
                            {user.role_id || "default-user"}
                          </code>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col items-start gap-1.5">
                          <Badge variant={user.provider === "linuxdo" ? "info" : "secondary"} className="rounded-md">
                            {providerLabel(user.provider)}
                            {user.provider === "linuxdo" && (() => {
                              const level = String(user.linuxdo_level || "").trim();
                              return level ? (
                                <span className={`ml-1 ${linuxDoLevelColors[level] || "text-muted-foreground"}`}>· Lv{level}</span>
                              ) : null;
                            })()}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={user.enabled ? "success" : "danger"} className="rounded-md">
                          {user.enabled ? "已启用" : "已禁用"}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1">
                          <div className="text-base font-semibold text-foreground">{numeric(user.quota_used)}</div>
                          <div className="text-xs text-muted-foreground">今日 {todayQuotaUsed(user)}</div>
                        </div>
                      </TableCell>
                      <TableCell className="w-[340px]">
                        <div className="flex items-center gap-4">
                          <UsageSparkline points={user.usage_curve} />
                          <div className="min-w-[72px] space-y-1 text-xs text-muted-foreground">
                            <div>总计 {formatCompactNumber(user.call_count)}</div>
                            <div>今日 {formatCompactNumber(todayCallCount(user))}</div>
                            <div>失败 {formatCompactNumber(user.failure_count)}</div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1 text-xs">
                          <div>创建 {formatDateTime(user.created_at)}</div>
                          <div>使用 {formatDateTime(user.last_used_at)}</div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg px-3"
                            onClick={() => openRoleDialog(user)}
                            disabled={isPending}
                          >
                            <ShieldCheck className="size-4" />
                            角色
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg px-3"
                            onClick={() => openEditDialog(user)}
                            disabled={isPending}
                          >
                            <Pencil className="size-4" />
                            编辑
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg px-3"
                            onClick={() => void handleToggle(user)}
                            disabled={isPending}
                          >
                            {isPending ? (
                              <LoaderCircle className="size-4 animate-spin" />
                            ) : user.enabled ? (
                              <Ban className="size-4" />
                            ) : (
                              <CheckCircle2 className="size-4" />
                            )}
                            {user.enabled ? "禁用" : "启用"}
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-8 rounded-lg border-rose-200 px-3 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                            onClick={() => setDeletingUser(user)}
                            disabled={isPending}
                          >
                            <Trash2 className="size-4" />
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          {isLoading ? (
            <div className="flex items-center justify-center py-14">
              <LoaderCircle className="size-5 animate-spin text-stone-400" />
            </div>
          ) : null}
          {!isLoading && filteredItems.length === 0 ? <div className="px-6 py-14 text-center text-sm text-stone-500">{items.length === 0 ? "暂无用户" : "没有匹配的用户"}</div> : null}
        </CardContent>
      </Card>

      <Dialog open={isCreateDialogOpen} onOpenChange={closeCreateDialog}>
        <DialogContent className="rounded-2xl p-6 sm:max-w-2xl">
          <DialogHeader className="gap-2">
            <DialogTitle>创建用户</DialogTitle>
            <DialogDescription className="text-sm leading-6">创建本地登录用户并绑定角色。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">用户名</label>
              <div className="relative">
                <UserRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={createForm.username}
                  onChange={(event) => updateCreateField("username", event.target.value.toLowerCase())}
                  placeholder="例如：operator_01"
                  autoComplete="username"
                  className="h-11 rounded-xl pl-9"
                  aria-invalid={Boolean(createErrors.username)}
                />
              </div>
              {createErrors.username ? <p className="text-xs leading-5 text-destructive">{createErrors.username}</p> : null}
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">显示名称</label>
              <Input
                value={createForm.name}
                onChange={(event) => updateCreateField("name", event.target.value)}
                placeholder="例如：运营账号"
                className="h-11 rounded-xl"
              />
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">密码</label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={createForm.password}
                  onChange={(event) => updateCreateField("password", event.target.value)}
                  placeholder="至少 8 位"
                  type="password"
                  autoComplete="new-password"
                  className="h-11 rounded-xl pl-9"
                  aria-invalid={Boolean(createErrors.password)}
                />
              </div>
              {createErrors.password ? <p className="text-xs leading-5 text-destructive">{createErrors.password}</p> : null}
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">确认密码</label>
              <Input
                value={createForm.confirmPassword}
                onChange={(event) => updateCreateField("confirmPassword", event.target.value)}
                placeholder="再次输入密码"
                type="password"
                autoComplete="new-password"
                className="h-11 rounded-xl"
                aria-invalid={Boolean(createErrors.confirmPassword)}
              />
              {createErrors.confirmPassword ? <p className="text-xs leading-5 text-destructive">{createErrors.confirmPassword}</p> : null}
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">角色</label>
              <Select value={createForm.role_id} onValueChange={(value) => updateCreateField("role_id", value)}>
                <SelectTrigger className="h-11 rounded-xl">
                  <SelectValue placeholder="选择角色" />
                </SelectTrigger>
                <SelectContent>
                  {roles.map((role) => (
                    <SelectItem key={role.id} value={role.id}>
                      {role.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">状态</label>
              <Select value={createForm.enabled ? "true" : "false"} onValueChange={(value) => updateCreateField("enabled", value === "true")}>
                <SelectTrigger className="h-11 rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="true">已启用</SelectItem>
                  <SelectItem value="false">已禁用</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="secondary" className="h-10 rounded-xl px-5" onClick={() => closeCreateDialog(false)} disabled={isCreating}>
              取消
            </Button>
            <Button type="button" className="h-10 rounded-xl px-5" onClick={() => void handleCreate()} disabled={isCreating}>
              {isCreating ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}
              创建
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(editingUser)} onOpenChange={(open) => (!open ? setEditingUser(null) : null)}>
        <DialogContent className="rounded-2xl p-6 sm:max-w-2xl">
          <DialogHeader className="gap-2">
            <DialogTitle className="flex items-center gap-2">
              <Pencil className="size-5 text-[#1456f0]" />
              编辑用户
            </DialogTitle>
            <DialogDescription className="truncate text-sm">
              {editingUser?.username || editingUser?.name || "用户"} · {editingUser?.id}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">显示名称</label>
              <Input
                value={editForm.name}
                onChange={(event) => updateEditField("name", event.target.value)}
                placeholder="显示名称"
                className="h-11 rounded-xl"
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">重置密码</label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={editForm.password}
                  onChange={(event) => updateEditField("password", event.target.value)}
                  placeholder="留空则不修改"
                  type="password"
                  autoComplete="new-password"
                  className="h-11 rounded-xl pl-9"
                  aria-invalid={Boolean(editErrors.password)}
                />
              </div>
              {editErrors.password ? <p className="text-xs leading-5 text-destructive">{editErrors.password}</p> : null}
            </div>
          </div>
          <div className="rounded-xl border border-border bg-muted/20 p-4 text-sm text-muted-foreground">
            <div className="grid gap-3 sm:grid-cols-3">
              <div>
                <div className="text-xs">当前总额度</div>
                <div className="mt-1 text-lg font-semibold text-foreground">{formatQuotaLimit(editingUser?.image_quota_total)}</div>
              </div>
              <div>
                <div className="text-xs">已用</div>
                <div className="mt-1 text-lg font-semibold text-foreground">{numeric(editingUser?.image_quota_used)}</div>
              </div>
              <div>
                <div className="text-xs">剩余</div>
                <div className="mt-1 text-lg font-semibold text-foreground">{formatQuotaLimit(editingUser?.image_quota_remaining)}</div>
              </div>
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">设置总点数</label>
              <Input
                value={editForm.quotaTotal}
                onChange={(event) => updateEditField("quotaTotal", event.target.value)}
                placeholder="留空不改；例如 100"
                inputMode="numeric"
                className="h-11 rounded-xl"
                aria-invalid={Boolean(editErrors.quotaTotal)}
              />
              {editErrors.quotaTotal ? <p className="text-xs leading-5 text-destructive">{editErrors.quotaTotal}</p> : null}
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium text-stone-700 dark:text-foreground">增减点数</label>
              <Input
                value={editForm.quotaDelta}
                onChange={(event) => updateEditField("quotaDelta", event.target.value)}
                placeholder="加 10 填 10，扣 5 填 -5"
                inputMode="numeric"
                className="h-11 rounded-xl"
                aria-invalid={Boolean(editErrors.quotaDelta)}
              />
              {editErrors.quotaDelta ? <p className="text-xs leading-5 text-destructive">{editErrors.quotaDelta}</p> : null}
            </div>
          </div>
          <p className="text-xs leading-5 text-muted-foreground">修改密码会让该用户现有本地登录会话失效，需使用新密码重新登录。</p>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl px-5"
              onClick={() => setEditingUser(null)}
              disabled={isSavingEdit}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl px-5"
              onClick={() => void handleSaveEdit()}
              disabled={isSavingEdit || !editingUser}
            >
              {isSavingEdit ? <LoaderCircle className="size-4 animate-spin" /> : <Pencil className="size-4" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(roleUser)} onOpenChange={(open) => (!open ? setRoleUser(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle className="flex items-center gap-2">
              <ShieldCheck className="size-5 text-[#1456f0]" />
              分配角色
            </DialogTitle>
            <DialogDescription className="truncate text-sm">
              {roleUser?.name || "普通用户"} · {roleUser?.id}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <label className="text-sm font-medium text-stone-700 dark:text-foreground">角色</label>
            <Select value={selectedRoleId} onValueChange={setSelectedRoleId}>
              <SelectTrigger className="h-11 rounded-xl">
                <SelectValue placeholder="选择角色" />
              </SelectTrigger>
              <SelectContent>
                {roles.map((role) => (
                  <SelectItem key={role.id} value={role.id}>
                    {role.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl px-5"
              onClick={() => setRoleUser(null)}
              disabled={isSavingRole}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl px-5"
              onClick={() => void handleSaveRole()}
              disabled={isSavingRole || !roleUser || !selectedRoleId}
            >
              {isSavingRole ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldCheck className="size-4" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deletingUser)} onOpenChange={(open) => (!open ? setDeletingUser(null) : null)}>
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除用户</DialogTitle>
            <DialogDescription className="text-sm leading-6">确认删除「{deletingUser?.name}」吗？</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              className="h-10 rounded-xl px-5"
              onClick={() => setDeletingUser(null)}
              disabled={deletingUser ? pendingIds.has(deletingUser.id) : false}
            >
              取消
            </Button>
            <Button
              type="button"
              className="h-10 rounded-xl bg-rose-600 px-5 text-white hover:bg-rose-700"
              onClick={() => void handleDelete()}
              disabled={deletingUser ? pendingIds.has(deletingUser.id) : false}
            >
              {deletingUser && pendingIds.has(deletingUser.id) ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

export default function UsersPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/users");
  if (isCheckingAuth || !session) {
    return <div className="flex min-h-[40vh] items-center justify-center"><LoaderCircle className="size-5 animate-spin text-stone-400" /></div>;
  }
  return <UsersContent />;
}
