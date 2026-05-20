"use client";

import { useEffect, useRef, useState } from "react";
import {
  Edit3,
  LoaderCircle,
  Megaphone,
  Plus,
  Save,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { AnnouncementMarkdown } from "@/components/announcement-markdown";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  createAnnouncement,
  deleteAnnouncement,
  fetchAdminAnnouncements,
  updateAnnouncement,
  type Announcement,
} from "@/lib/api";

import {
  SettingsCard,
  SettingsEmptyState,
  settingsDialogInputClassName,
  settingsListItemClassName,
  settingsToggleClassName,
} from "./settings-ui";

type AnnouncementForm = {
  title: string;
  content: string;
  enabled: boolean;
  show_login: boolean;
  show_image: boolean;
};

const emptyForm: AnnouncementForm = {
  title: "",
  content: "",
  enabled: true,
  show_login: true,
  show_image: true,
};

function formatDateTime(value?: string | null) {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function formFromAnnouncement(item: Announcement): AnnouncementForm {
  return {
    title: item.title || "",
    content: item.content || "",
    enabled: Boolean(item.enabled),
    show_login: Boolean(item.show_login),
    show_image: Boolean(item.show_image),
  };
}

export function AnnouncementsCard() {
  const didLoadRef = useRef(false);
  const contentTextareaRef = useRef<HTMLTextAreaElement | null>(null);
  const [items, setItems] = useState<Announcement[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [pendingIds, setPendingIds] = useState<Set<string>>(() => new Set());
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<Announcement | null>(null);
  const [deletingItem, setDeletingItem] = useState<Announcement | null>(null);
  const [form, setForm] = useState<AnnouncementForm>(emptyForm);

  const load = async () => {
    setIsLoading(true);
    try {
      const data = await fetchAdminAnnouncements();
      setItems(data.items);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载公告失败");
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

  const openCreateDialog = () => {
    setEditingItem(null);
    setForm(emptyForm);
    setDialogOpen(true);
  };

  const openEditDialog = (item: Announcement) => {
    setEditingItem(item);
    setForm(formFromAnnouncement(item));
    setDialogOpen(true);
  };

  const updateForm = (updates: Partial<AnnouncementForm>) => {
    setForm((current) => ({ ...current, ...updates }));
  };

  const insertMarkdownSnippet = (snippet: string) => {
    const textarea = contentTextareaRef.current;
    if (!textarea) {
      updateForm({ content: `${form.content}${form.content ? "\n" : ""}${snippet}` });
      return;
    }
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const nextContent = `${form.content.slice(0, start)}${snippet}${form.content.slice(end)}`;
    updateForm({ content: nextContent });
    window.requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start + snippet.length, start + snippet.length);
    });
  };

  const handleSave = async () => {
    const payload = {
      title: form.title.trim(),
      content: form.content.trim(),
      enabled: form.enabled,
      show_login: form.show_login,
      show_image: form.show_image,
    };
    if (!payload.content) {
      toast.error("请输入公告内容");
      return;
    }
    if (!payload.show_login && !payload.show_image) {
      toast.error("请选择至少一个展示位置");
      return;
    }

    setIsSaving(true);
    try {
      const data = editingItem
        ? await updateAnnouncement(editingItem.id, payload)
        : await createAnnouncement(payload);
      setItems(data.items);
      setDialogOpen(false);
      setEditingItem(null);
      setForm(emptyForm);
      toast.success(editingItem ? "公告已更新" : "公告已创建");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存公告失败");
    } finally {
      setIsSaving(false);
    }
  };

  const handleToggleEnabled = async (item: Announcement) => {
    setItemPending(item.id, true);
    try {
      const data = await updateAnnouncement(item.id, {
        enabled: !item.enabled,
      });
      setItems(data.items);
      toast.success(item.enabled ? "公告已停用" : "公告已启用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新公告失败");
    } finally {
      setItemPending(item.id, false);
    }
  };

  const handleDelete = async () => {
    if (!deletingItem) {
      return;
    }
    const item = deletingItem;
    setItemPending(item.id, true);
    try {
      const data = await deleteAnnouncement(item.id);
      setItems(data.items);
      setDeletingItem(null);
      toast.success("公告已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除公告失败");
    } finally {
      setItemPending(item.id, false);
    }
  };

  return (
    <>
      <SettingsCard
        icon={Megaphone}
        title="公告管理"
        description="创建多条公告，并分别选择显示在登录页或创作台。"
        tone="amber"
        action={
          <Button onClick={openCreateDialog}>
            <Plus data-icon="inline-start" />
            添加公告
          </Button>
        }
      >
        <div className="flex flex-col gap-6">
          {isLoading ? (
            <div className="flex items-center justify-center py-10">
              <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
            </div>
          ) : items.length === 0 ? (
            <SettingsEmptyState
              icon={Megaphone}
              title="暂无公告"
              description="添加后可选择在登录页、创作台或两个位置同时展示。"
            />
          ) : (
            <div className="flex flex-col gap-3">
              {items.map((item) => {
                const isPending = pendingIds.has(item.id);
                return (
                  <div
                    key={item.id}
                    className={`flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between ${settingsListItemClassName}`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <div className="truncate text-sm font-semibold text-foreground">
                          {item.title || "公告"}
                        </div>
                        <Badge variant={item.enabled ? "success" : "secondary"}>
                          {item.enabled ? "已启用" : "已停用"}
                        </Badge>
                        {item.show_login ? (
                          <Badge variant="warning">登录页</Badge>
                        ) : null}
                        {item.show_image ? (
                          <Badge variant="info">创作台</Badge>
                        ) : null}
                      </div>
                      <AnnouncementMarkdown className="mt-2 line-clamp-2 text-muted-foreground" compact>{item.content}</AnnouncementMarkdown>
                      <div className="mt-2 text-xs text-muted-foreground/80">
                        更新于 {formatDateTime(item.updated_at)}
                      </div>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        onClick={() => void handleToggleEnabled(item)}
                        disabled={isPending}
                      >
                        {isPending ? (
                          <LoaderCircle
                            data-icon="inline-start"
                            className="animate-spin"
                          />
                        ) : null}
                        {item.enabled ? "停用" : "启用"}
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        onClick={() => openEditDialog(item)}
                        disabled={isPending}
                      >
                        <Edit3 data-icon="inline-start" />
                        编辑
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        className="border-rose-200 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                        onClick={() => setDeletingItem(item)}
                        disabled={isPending}
                      >
                        <Trash2 data-icon="inline-start" />
                        删除
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </SettingsCard>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[min(92vh,760px)] overflow-hidden rounded-2xl p-0">
          <DialogHeader className="gap-2 px-6 pb-2 pt-6">
            <DialogTitle>{editingItem ? "编辑公告" : "添加公告"}</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              公告内容会按勾选位置显示给对应页面的用户。
            </DialogDescription>
          </DialogHeader>
          <FieldGroup className="max-h-[min(62vh,520px)] overflow-y-auto px-6 py-4">
            <Field>
              <FieldLabel htmlFor="announcement-title">标题</FieldLabel>
              <Input
                id="announcement-title"
                value={form.title}
                onChange={(event) => updateForm({ title: event.target.value })}
                placeholder="公告"
                className={settingsDialogInputClassName}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="announcement-content">内容</FieldLabel>
              <Textarea
                ref={contentTextareaRef}
                id="announcement-content"
                value={form.content}
                onChange={(event) =>
                  updateForm({ content: event.target.value })
                }
                placeholder={"### 更新公告\n支持加粗、链接、列表等 Markdown 语法。"}
                className="min-h-36 bg-background"
              />
              <FieldDescription>
                支持 Markdown：标题（#）、列表（- / 1.）、加粗（**）、引用（&gt;）、链接（[文本](URL)）。
              </FieldDescription>
              <div className="flex flex-wrap gap-2">
                {[
                  ["标题", "### 公告标题"],
                  ["加粗", "**重点内容**"],
                  ["链接", "[查看详情](https://images.dfmcn.com)"],
                  ["列表", "- 第一条\n- 第二条"],
                  ["引用", "> 重要提醒内容"],
                ].map(([label, snippet]) => (
                  <Button
                    key={label}
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => insertMarkdownSnippet(snippet)}
                  >
                    {label}
                  </Button>
                ))}
              </div>
              {form.content.trim() ? (
                <div className="rounded-2xl border border-amber-200 bg-amber-50/70 p-4">
                  <div className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-amber-700">
                    Markdown 预览
                  </div>
                  <AnnouncementMarkdown className="text-stone-800">{form.content}</AnnouncementMarkdown>
                </div>
              ) : null}
            </Field>
            <div className="grid gap-3 md:grid-cols-3">
              <label className={settingsToggleClassName}>
                <Checkbox
                  checked={form.enabled}
                  onCheckedChange={(checked) =>
                    updateForm({ enabled: Boolean(checked) })
                  }
                />
                启用公告
              </label>
              <label className={settingsToggleClassName}>
                <Checkbox
                  checked={form.show_login}
                  onCheckedChange={(checked) =>
                    updateForm({ show_login: Boolean(checked) })
                  }
                />
                登录页显示
              </label>
              <label className={settingsToggleClassName}>
                <Checkbox
                  checked={form.show_image}
                  onCheckedChange={(checked) =>
                    updateForm({ show_image: Boolean(checked) })
                  }
                />
                创作台显示
              </label>
            </div>
          </FieldGroup>
          <DialogFooter className="border-t border-border bg-background px-6 py-4">
            <Button
              type="button"
              variant="secondary"
              size="lg"
              onClick={() => setDialogOpen(false)}
              disabled={isSaving}
            >
              取消
            </Button>
            <Button
              type="button"
              size="lg"
              onClick={() => void handleSave()}
              disabled={isSaving}
            >
              {isSaving ? (
                <LoaderCircle
                  data-icon="inline-start"
                  className="animate-spin"
                />
              ) : (
                <Save data-icon="inline-start" />
              )}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={Boolean(deletingItem)}
        onOpenChange={(open) => (!open ? setDeletingItem(null) : null)}
      >
        <DialogContent className="rounded-2xl p-6">
          <DialogHeader className="gap-2">
            <DialogTitle>删除公告</DialogTitle>
            <DialogDescription className="text-sm leading-6">
              确认删除公告「{deletingItem?.title || "公告"}
              」吗？删除后不会再显示在任何页面。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              size="lg"
              onClick={() => setDeletingItem(null)}
              disabled={deletingItem ? pendingIds.has(deletingItem.id) : false}
            >
              取消
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="lg"
              onClick={() => void handleDelete()}
              disabled={deletingItem ? pendingIds.has(deletingItem.id) : false}
            >
              {deletingItem && pendingIds.has(deletingItem.id) ? (
                <LoaderCircle
                  data-icon="inline-start"
                  className="animate-spin"
                />
              ) : (
                <Trash2 data-icon="inline-start" />
              )}
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
