"use client";

import { useEffect, useState } from "react";
import { Bell, Megaphone } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AnnouncementMarkdown } from "@/components/announcement-markdown";
import { fetchVisibleAnnouncements, type Announcement, type AnnouncementTarget } from "@/lib/api";
import { cn } from "@/lib/utils";

export function AnnouncementNotifications({
  target,
  className,
  autoDialog = false,
}: {
  target: AnnouncementTarget;
  className?: string;
  autoDialog?: boolean;
}) {
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [dialogOpen, setDialogOpen] = useState(false);

  useEffect(() => {
    let active = true;

    const loadAnnouncements = async () => {
      try {
        const data = await fetchVisibleAnnouncements(target);
        if (active) {
          setAnnouncements(data.items);
        }
      } catch {
        if (active) {
          setAnnouncements([]);
        }
      }
    };

    void loadAnnouncements();
    return () => {
      active = false;
    };
  }, [target]);

  useEffect(() => {
    if (!autoDialog || announcements.length === 0) {
      return;
    }
    const timer = window.setTimeout(() => {
      setDialogOpen(true);
    }, 700);
    return () => window.clearTimeout(timer);
  }, [announcements, autoDialog]);

  if (announcements.length === 0) {
    return null;
  }

  return (
    <>
      <Popover>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            className={cn(
              "relative size-10 rounded-full border-amber-200 bg-amber-50/95 p-0 text-amber-800 shadow-sm hover:bg-amber-100 hover:text-amber-900",
              className,
            )}
            aria-label={`查看 ${announcements.length} 条公告`}
          >
            <Bell className="size-4" />
            <span className="absolute -right-1 -top-1 flex min-w-5 items-center justify-center rounded-full bg-amber-600 px-1.5 text-[10px] font-semibold leading-5 text-white">
              {announcements.length}
            </span>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-[min(calc(100vw-2rem),400px)] p-0">
          <div className="border-b border-stone-100 px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-stone-900">
              <Bell className="size-4 text-amber-700" />
              通知提醒
            </div>
          </div>
          <div aria-live="polite" className="max-h-[min(64vh,520px)] overflow-y-auto p-3">
            <div className="flex flex-col gap-3">
              {announcements.map((announcement) => (
                <aside
                  key={announcement.id}
                  className="flex gap-3 rounded-2xl border border-amber-200/80 bg-amber-50/90 px-4 py-3 text-left"
                >
                  <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl bg-amber-100 text-amber-800">
                    <Megaphone className="size-4" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold text-stone-900">{announcement.title.trim() || "公告"}</p>
                    <AnnouncementMarkdown className="mt-1 text-stone-700">{announcement.content}</AnnouncementMarkdown>
                  </div>
                </aside>
              ))}
            </div>
          </div>
        </PopoverContent>
      </Popover>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[min(86vh,680px)] overflow-hidden p-0">
          <div className="border-b border-amber-100 bg-gradient-to-br from-amber-50 via-white to-sky-50 px-6 py-5">
            <DialogHeader>
              <div className="flex size-11 items-center justify-center rounded-2xl bg-amber-500 text-white shadow-[0_12px_28px_rgba(245,158,11,0.24)]">
                <Megaphone className="size-5" />
              </div>
              <DialogTitle className="text-2xl tracking-[-0.03em] text-stone-950">网站公告</DialogTitle>
              <DialogDescription>请查看最新通知，关闭后仍可通过顶部铃铛再次打开。</DialogDescription>
            </DialogHeader>
          </div>
          <div className="max-h-[min(58vh,440px)] overflow-y-auto px-5 py-4">
            <div className="flex flex-col gap-3">
              {announcements.map((announcement) => (
                <aside key={announcement.id} className="rounded-2xl border border-stone-200 bg-white px-4 py-3 shadow-sm">
                  <p className="text-base font-semibold text-stone-950">{announcement.title.trim() || "公告"}</p>
                  <AnnouncementMarkdown className="mt-2 text-stone-700">{announcement.content}</AnnouncementMarkdown>
                </aside>
              ))}
            </div>
          </div>
          <DialogFooter className="border-t border-stone-100 bg-stone-50 px-5 py-4">
            <Button type="button" onClick={() => setDialogOpen(false)} className="rounded-full px-6">
              我知道了
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
