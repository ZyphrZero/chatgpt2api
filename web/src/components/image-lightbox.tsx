"use client";

import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ChevronLeft, ChevronRight, Download, RotateCcw, X, ZoomIn, ZoomOut } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { cn } from "@/lib/utils";

type LightboxImage = {
  id: string;
  src: string;
  sizeLabel?: string;
  dimensions?: string;
};

type ImageLightboxProps = {
  images: LightboxImage[];
  currentIndex: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onIndexChange: (index: number) => void;
};

const MIN_ZOOM = 1;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.25;

export function ImageLightbox({
  images,
  currentIndex,
  open,
  onOpenChange,
  onIndexChange,
}: ImageLightboxProps) {
  const current = images[currentIndex];
  const hasPrev = currentIndex > 0;
  const hasNext = currentIndex < images.length - 1;
  const [zoom, setZoom] = useState(MIN_ZOOM);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{ pointerId: number; startX: number; startY: number; panX: number; panY: number } | null>(null);
  const zoomPercent = Math.round(zoom * 100);

  const resetZoom = useCallback(() => {
    setZoom(MIN_ZOOM);
    setPan({ x: 0, y: 0 });
  }, []);

  const zoomIn = useCallback(() => {
    setZoom((value) => Math.min(MAX_ZOOM, value + ZOOM_STEP));
  }, []);

  const zoomOut = useCallback(() => {
    setZoom((value) => {
      const next = Math.max(MIN_ZOOM, value - ZOOM_STEP);
      if (next === MIN_ZOOM) {
        setPan({ x: 0, y: 0 });
      }
      return next;
    });
  }, []);

  const goPrev = useCallback(() => {
    if (hasPrev) onIndexChange(currentIndex - 1);
  }, [hasPrev, currentIndex, onIndexChange]);

  const goNext = useCallback(() => {
    if (hasNext) onIndexChange(currentIndex + 1);
  }, [hasNext, currentIndex, onIndexChange]);

  useEffect(() => {
    if (!open) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") {
        e.preventDefault();
        goPrev();
      } else if (e.key === "ArrowRight") {
        e.preventDefault();
        goNext();
      } else if (e.key === "+" || e.key === "=") {
        e.preventDefault();
        zoomIn();
      } else if (e.key === "-") {
        e.preventDefault();
        zoomOut();
      } else if (e.key === "0") {
        e.preventDefault();
        resetZoom();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [open, goPrev, goNext, resetZoom, zoomIn, zoomOut]);

  useEffect(() => {
    if (open) {
      resetZoom();
    }
  }, [current?.id, open, resetZoom]);

  const handleDownload = useCallback(() => {
    if (!current) return;
    const link = document.createElement("a");
    link.href = current.src;
    link.download = `image-${current.id}.png`;
    link.click();
  }, [current]);

  const handleImagePointerDown = useCallback(
    (event: React.PointerEvent<HTMLImageElement>) => {
      event.stopPropagation();
      if (zoom <= MIN_ZOOM) {
        return;
      }
      event.currentTarget.setPointerCapture(event.pointerId);
      dragRef.current = {
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        panX: pan.x,
        panY: pan.y,
      };
    },
    [pan.x, pan.y, zoom],
  );

  const handleImagePointerMove = useCallback((event: React.PointerEvent<HTMLImageElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    event.preventDefault();
    setPan({
      x: drag.panX + event.clientX - drag.startX,
      y: drag.panY + event.clientY - drag.startY,
    });
  }, []);

  const handleImagePointerEnd = useCallback((event: React.PointerEvent<HTMLImageElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) {
      dragRef.current = null;
    }
  }, []);

  if (!current) return null;

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/80 backdrop-blur-sm data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
        <DialogPrimitive.Content
          className="fixed inset-0 z-50 flex items-center justify-center outline-none"
          onPointerDownOutside={(e) => e.preventDefault()}
        >
          <DialogPrimitive.Title className="sr-only">
            图片预览
          </DialogPrimitive.Title>

          <div className="absolute top-3 right-3 left-3 z-20 flex flex-wrap items-center justify-end gap-2 sm:top-4 sm:right-4 sm:left-auto">
            {current.sizeLabel || current.dimensions ? (
              <span className="max-w-[calc(100vw-2rem)] truncate rounded-full bg-black/50 px-3 py-1.5 text-xs font-medium text-white/90 sm:max-w-[42vw]">
                {[current.sizeLabel, current.dimensions].filter(Boolean).join(" · ")}
              </span>
            ) : null}
            {images.length > 1 && (
              <span className="rounded-full bg-black/50 px-3 py-1.5 text-xs font-medium text-white/90">
                {currentIndex + 1} / {images.length}
              </span>
            )}
            <div className="flex items-center rounded-full bg-black/50 text-white/90">
              <button
                type="button"
                onClick={zoomOut}
                disabled={zoom <= MIN_ZOOM}
                className="inline-flex size-9 items-center justify-center rounded-l-full transition hover:bg-black/40 disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="缩小图片"
                title="缩小图片"
              >
                <ZoomOut className="size-4" />
              </button>
              <span className="min-w-12 text-center text-xs font-medium tabular-nums">{zoomPercent}%</span>
              <button
                type="button"
                onClick={zoomIn}
                disabled={zoom >= MAX_ZOOM}
                className="inline-flex size-9 items-center justify-center transition hover:bg-black/40 disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="放大图片"
                title="放大图片"
              >
                <ZoomIn className="size-4" />
              </button>
              <button
                type="button"
                onClick={resetZoom}
                disabled={zoom === MIN_ZOOM && pan.x === 0 && pan.y === 0}
                className="inline-flex size-9 items-center justify-center rounded-r-full transition hover:bg-black/40 disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="重置缩放"
                title="重置缩放"
              >
                <RotateCcw className="size-4" />
              </button>
            </div>
            <button
              type="button"
              onClick={handleDownload}
              className="inline-flex size-9 items-center justify-center rounded-full bg-black/50 text-white/90 transition hover:bg-black/70"
              aria-label="下载图片"
            >
              <Download className="size-4" />
            </button>
            <DialogPrimitive.Close className="inline-flex size-9 items-center justify-center rounded-full bg-black/50 text-white/90 transition hover:bg-black/70">
              <X className="size-4" />
              <span className="sr-only">关闭</span>
            </DialogPrimitive.Close>
          </div>

          {hasPrev && (
            <button
              type="button"
              onClick={goPrev}
              className="absolute left-4 z-10 inline-flex size-10 items-center justify-center rounded-full bg-black/40 text-white/90 transition hover:bg-black/60"
              aria-label="上一张"
            >
              <ChevronLeft className="size-5" />
            </button>
          )}

          <div
            className="flex h-full w-full items-center justify-center overflow-hidden p-4 pt-24 sm:p-8 sm:pt-20"
            onClick={() => onOpenChange(false)}
          >
            <img
              src={current.src}
              alt={`图片预览 ${currentIndex + 1}`}
              className={cn(
                "max-h-[90vh] max-w-[90vw] rounded-lg object-contain transition-transform duration-100",
                zoom > MIN_ZOOM ? "cursor-grab touch-none active:cursor-grabbing" : "cursor-zoom-in",
              )}
              style={{ transform: `translate3d(${pan.x}px, ${pan.y}px, 0) scale(${zoom})` }}
              onClick={(event) => {
                event.stopPropagation();
                if (zoom === MIN_ZOOM) {
                  zoomIn();
                }
              }}
              onPointerDown={handleImagePointerDown}
              onPointerMove={handleImagePointerMove}
              onPointerUp={handleImagePointerEnd}
              onPointerCancel={handleImagePointerEnd}
              draggable={false}
            />
          </div>

          {hasNext && (
            <button
              type="button"
              onClick={goNext}
              className="absolute right-4 z-10 inline-flex size-10 items-center justify-center rounded-full bg-black/40 text-white/90 transition hover:bg-black/60"
              aria-label="下一张"
            >
              <ChevronRight className="size-5" />
            </button>
          )}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
