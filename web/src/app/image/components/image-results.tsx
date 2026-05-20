"use client";

import { useRef, useState } from "react";
import { Check, CircleStop, Clock3, Download, Eye, Globe2, Images, LoaderCircle, Lock, PencilLine, Plus, RotateCcw, Share2, Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import type { ImagePromptPreset } from "@/app/image/image-presets";
import { createImageShare, type ImageShare, type ImageVisibility } from "@/lib/api";
import { formatBase64ImageFileSize, formatImageFileSize } from "@/lib/image-size";
import { cn } from "@/lib/utils";
import type { ImageConversation, ImageTurn, ImageTurnStatus, StoredImage, StoredReferenceImage } from "@/store/image-conversations";
import type { ImageTurnProgress } from "@/store/image-turn-progress";

const INITIAL_VISIBLE_DESKTOP_TURN_COUNT = 4;
const INITIAL_VISIBLE_MOBILE_TURN_COUNT = 1;
const INITIAL_VISIBLE_PRESET_COUNT = 2;

export type ImageLightboxItem = {
  id: string;
  src: string;
  sizeLabel?: string;
  dimensions?: string;
};

type DownloadableImage = {
  id: string;
  selectionKey: string;
  src: string;
  fileName: string;
  imageIndex: number;
};

type ImageResultsProps = {
  selectedConversation: ImageConversation | null;
  progressByTurnKey: Record<string, ImageTurnProgress>;
  progressNow: number;
  promptPresets: readonly ImagePromptPreset[];
  isLoadingPromptPresets?: boolean;
  promptPresetStatus?: string;
  onOpenLightbox: (images: ImageLightboxItem[], index: number) => void;
  onApplyPromptPreset: (preset: ImagePromptPreset) => void | Promise<void>;
  onContinueEdit: (conversationId: string, image: StoredImage | StoredReferenceImage) => void;
  onEditTurn: (conversationId: string, turnId: string) => void;
  onCancelTurn: (conversationId: string, turnId: string) => void | Promise<void>;
  onRegenerateTurn: (conversationId: string, turnId: string) => void | Promise<void>;
  onRetryImage: (conversationId: string, turnId: string, imageIndex: number) => void | Promise<void>;
  onImageVisibilityChange: (
    conversationId: string,
    turnId: string,
    imageIndex: number,
    visibility: ImageVisibility,
  ) => void | Promise<void>;
  visibilityMutatingImageKey: string;
  formatConversationTime: (value: string) => string;
};

function getStoredImageSrc(image: StoredImage) {
  if (image.b64_json) {
    return `data:image/${image.outputFormat || "png"};base64,${image.b64_json}`;
  }
  return image.url || "";
}

function isTurnBusy(turn: ImageTurn) {
  return (
    turn.status === "queued" ||
    turn.status === "generating" ||
    turn.images.some((image) => image.status === "loading")
  );
}

function imageSelectionKey(conversationId: string, turnId: string, imageId: string) {
  return `${conversationId}:${turnId}:${imageId}`;
}

function getImageFormatLabel(image: StoredImage, src: string) {
  const dataUrlFormat = src.match(/^data:image\/([^;,]+)/i)?.[1];
  const urlFormat = image.url ? image.url.split("?")[0]?.match(/\.([a-z0-9]+)$/i)?.[1] : "";
  const normalized = String(dataUrlFormat || urlFormat || (image.b64_json ? "png" : "png")).toLowerCase();
  const format = normalized === "jpeg" ? "jpg" : normalized;
  return `IMAGE ${format.toUpperCase()}`;
}

function imageResolutionLabel(image: StoredImage, dimensions?: string) {
  if (image.resolution) {
    return image.resolution.replace(/x/g, " x ");
  }
  if (image.width && image.height) {
    return formatImageDimensions(image.width, image.height);
  }
  return dimensions || "";
}

function getTurnResultSizeLabel(turn: ImageTurn, dimensionsByImageId: Record<string, string>) {
  const labels = Array.from(
    new Set(
      turn.images
        .filter((image) => image.status === "success")
        .map((image) => imageResolutionLabel(image, dimensionsByImageId[image.id]))
        .filter(Boolean),
    ),
  );
  if (labels.length === 1) {
    return labels[0];
  }
  if (labels.length > 1) {
    return `${labels.length} 种尺寸`;
  }
  return isTurnBusy(turn) && turn.size ? `请求 ${turn.size}` : "";
}

function imageVisibilityLabel(visibility?: ImageVisibility) {
  return visibility === "public" ? "已公开" : "私有";
}

function imageVisibilityPillClass(visibility?: ImageVisibility) {
  return visibility === "public"
    ? "bg-[#e8f2ff] text-[#1456f0] ring-1 ring-[#bfdbfe] dark:bg-[#21180d]/92 dark:text-[#ffcf73] dark:ring-[#d6aa56]/35"
    : "bg-[#181e25]/82 text-white ring-1 ring-white/20 dark:bg-[#0d0c09]/88 dark:text-[#f5e6bf] dark:ring-[#d6aa56]/24";
}

function imageVisibilityActionClass(visibility?: ImageVisibility) {
  return visibility === "public"
    ? "bg-white/95 text-[#1456f0] hover:bg-[#e8f2ff] dark:bg-[#15120d]/95 dark:text-[#ffcf73] dark:hover:bg-[#21180d]"
    : "bg-white/95 text-stone-800 hover:bg-stone-100 dark:bg-[#15120d]/95 dark:text-[#f5e6bf] dark:hover:bg-[#21180d]";
}

function blurFocusedElementInContainer(container: HTMLElement) {
  const activeElement = document.activeElement;
  if (activeElement instanceof HTMLElement && container.contains(activeElement)) {
    activeElement.blur();
  }
}

function imageExtension(outputFormat?: string) {
  return outputFormat === "jpeg" ? "jpg" : outputFormat || "png";
}

function buildDownloadName(createdAt: string, turnId: string, index: number, outputFormat?: string) {
  const date = new Date(createdAt);
  const safeIndex = String(index + 1).padStart(2, "0");
  const extension = imageExtension(outputFormat);
  if (Number.isNaN(date.getTime())) {
    return `chatgpt-image-${turnId.slice(0, 8)}-${safeIndex}.${extension}`;
  }

  const yyyy = String(date.getFullYear());
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  const hh = String(date.getHours()).padStart(2, "0");
  const min = String(date.getMinutes()).padStart(2, "0");
  const sec = String(date.getSeconds()).padStart(2, "0");
  return `chatgpt-image-${yyyy}${mm}${dd}-${hh}${min}${sec}-${safeIndex}.${extension}`;
}

async function downloadImage(image: DownloadableImage) {
  let href = image.src;
  let objectUrl = "";

  if (!image.src.startsWith("data:")) {
    try {
      const response = await fetch(image.src);
      if (response.ok) {
        const blob = await response.blob();
        objectUrl = URL.createObjectURL(blob);
        href = objectUrl;
      }
    } catch {
      href = image.src;
    }
  }

  const link = document.createElement("a");
  link.href = href;
  link.download = image.fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();

  if (objectUrl) {
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
  }
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function shouldProbeImageFileSize() {
  return !window.matchMedia?.("(max-width: 768px), (pointer: coarse)").matches;
}

function getInitialVisibleTurnCount() {
  if (typeof window === "undefined") {
    return INITIAL_VISIBLE_DESKTOP_TURN_COUNT;
  }
  return window.matchMedia?.("(max-width: 768px), (pointer: coarse)").matches
    ? INITIAL_VISIBLE_MOBILE_TURN_COUNT
    : INITIAL_VISIBLE_DESKTOP_TURN_COUNT;
}

function shouldShowPromptPresetsInline() {
  if (typeof window === "undefined") {
    return true;
  }
  return !window.matchMedia?.("(max-width: 768px), (pointer: coarse)").matches;
}

function scheduleResultIdleWork(callback: () => void) {
  if ("requestIdleCallback" in window) {
    const idleId = window.requestIdleCallback(callback, { timeout: 1600 });
    return () => window.cancelIdleCallback(idleId);
  }
  const timer = globalThis.setTimeout(callback, 900);
  return () => globalThis.clearTimeout(timer);
}

async function fetchImageSizeLabel(src: string) {
  if (!src || src.startsWith("data:")) {
    return "";
  }

  try {
    const response = await fetch(src);
    if (!response.ok) {
      return "";
    }
    const blob = await response.blob();
    return formatImageFileSize(blob.size);
  } catch {
    return "";
  }
}

async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

function buildShareCopyText(url: string) {
  return `我在 1818 生成了一张图片，你可以打开链接生成同款:\n${url}`;
}

function shareFileName(image: StoredImage) {
  return `1818-image.${imageExtension(image.outputFormat)}`;
}

async function imageFileFromSrc(src: string, image: StoredImage) {
  const response = await fetch(src);
  if (!response.ok) {
    throw new Error("share image fetch failed");
  }
  const blob = await response.blob();
  const type = blob.type || `image/${image.outputFormat || "png"}`;
  return new File([blob], shareFileName(image), { type });
}

function canUseNativeShare(data: ShareData) {
  if (!navigator.share) {
    return false;
  }
  if (!navigator.canShare) {
    return true;
  }
  try {
    return navigator.canShare(data);
  } catch {
    return false;
  }
}

function shouldOpenSharePageFallback() {
  return window.matchMedia?.("(max-width: 640px), (pointer: coarse)").matches ?? false;
}

async function nativeShareImage(share: ImageShare, imageSrc: string, image: StoredImage) {
  if (!navigator.share) {
    return false;
  }
  const title = "1818 作品分享";
  const text = "我在 1818 生成了一张图片，你可以打开链接生成同款。";
  try {
    const file = await imageFileFromSrc(imageSrc, image);
    const data = { title, text, url: share.share_url, files: [file] };
    if (canUseNativeShare(data)) {
      await navigator.share(data);
      return true;
    }
  } catch {
    // Some in-app browsers forbid file sharing; URL sharing below still improves the flow.
  }

  const data = { title, text, url: share.share_url };
  if (!canUseNativeShare(data)) {
    return false;
  }
  await navigator.share(data);
  return true;
}

export function ImageResults({
  selectedConversation,
  progressByTurnKey,
  progressNow,
  promptPresets,
  isLoadingPromptPresets = false,
  promptPresetStatus = "",
  onOpenLightbox,
  onApplyPromptPreset,
  onContinueEdit,
  onEditTurn,
  onCancelTurn,
  onRegenerateTurn,
  onRetryImage,
  onImageVisibilityChange,
  visibilityMutatingImageKey,
  formatConversationTime,
}: ImageResultsProps) {
  const [imageDimensions, setImageDimensions] = useState<Record<string, string>>({});
  const [imageSizeLabels, setImageSizeLabels] = useState<Record<string, string>>({});
  const [selectedImageIds, setSelectedImageIds] = useState<Record<string, boolean>>({});
  const [downloadingKey, setDownloadingKey] = useState<string | null>(null);
  const [sharingImageKey, setSharingImageKey] = useState<string | null>(null);
  const [expandedConversationId, setExpandedConversationId] = useState("");
  const [showPromptPresets, setShowPromptPresets] = useState(shouldShowPromptPresetsInline);
  const [showAllPromptPresets, setShowAllPromptPresets] = useState(false);
  const pendingImageSizeIdsRef = useRef<Set<string>>(new Set());
  const visiblePromptPresets = showPromptPresets
    ? showAllPromptPresets
      ? promptPresets
      : promptPresets.slice(0, INITIAL_VISIBLE_PRESET_COUNT)
    : [];
  const hiddenPromptPresetCount = Math.max(0, promptPresets.length - visiblePromptPresets.length);
  const canTogglePromptPresets = showPromptPresets && promptPresets.length > INITIAL_VISIBLE_PRESET_COUNT;

  const updateImageDimensions = (id: string, width: number, height: number) => {
    const dimensions = formatImageDimensions(width, height);
    setImageDimensions((current) => {
      if (current[id] === dimensions) {
        return current;
      }
      return { ...current, [id]: dimensions };
    });
  };

  const toggleImageSelection = (selectionKey: string) => {
    setSelectedImageIds((current) => ({
      ...current,
      [selectionKey]: !current[selectionKey],
    }));
  };

  const updateImageSizeLabel = (id: string, sizeLabel: string) => {
    if (!sizeLabel) {
      return;
    }
    setImageSizeLabels((current) => {
      if (current[id] === sizeLabel) {
        return current;
      }
      return { ...current, [id]: sizeLabel };
    });
  };

  const ensureImageSizeLabel = (id: string, src: string) => {
    if (!shouldProbeImageFileSize() || imageSizeLabels[id] || pendingImageSizeIdsRef.current.has(id)) {
      return;
    }

    pendingImageSizeIdsRef.current.add(id);
    scheduleResultIdleWork(() => {
      void fetchImageSizeLabel(src)
        .then((sizeLabel) => updateImageSizeLabel(id, sizeLabel))
        .finally(() => {
          pendingImageSizeIdsRef.current.delete(id);
        });
    });
  };

  const downloadItems = async (key: string, items: DownloadableImage[]) => {
    if (items.length === 0 || downloadingKey) {
      return;
    }

    setDownloadingKey(key);
    try {
      for (let index = 0; index < items.length; index += 1) {
        await downloadImage(items[index]);
        if (index < items.length - 1) {
          await sleep(120);
        }
      }
    } finally {
      setDownloadingKey(null);
    }
  };

  const shareImage = async (turn: ImageTurn, image: StoredImage, index: number, imageSrc: string) => {
    if (!imageSrc || sharingImageKey) {
      return;
    }

    const shareKey = `${turn.id}:${image.id}`;
    setSharingImageKey(shareKey);
    try {
      const share = await createImageShare({
        image: imageSrc,
        prompt: turn.prompt,
        revised_prompt: image.revised_prompt || "",
        model: turn.model,
        size: image.resolution || turn.size || "",
        quality: turn.quality || "",
        result_index: index + 1,
      });
      try {
        if (await nativeShareImage(share, imageSrc, image)) {
          toast.success("已打开系统分享");
          return;
        }
      } catch (shareError) {
        if (shareError instanceof DOMException && shareError.name === "AbortError") {
          return;
        }
      }
      try {
        await copyText(buildShareCopyText(share.share_url));
        if (shouldOpenSharePageFallback()) {
          toast.success("分享页已创建，正在打开");
          window.location.href = share.share_url;
        } else {
          toast.success("分享文案已复制");
        }
      } catch {
        toast.success("分享页已创建，正在打开");
        window.location.href = share.share_url;
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建分享失败");
    } finally {
      setSharingImageKey(null);
    }
  };

  if (!selectedConversation) {
    return (
      <div className="flex h-full min-h-[220px] items-start justify-center px-0 py-1 text-center sm:min-h-[340px]">
        <div className="mx-auto flex w-full max-w-[1180px] flex-col gap-2 rounded-[18px] border border-white/[0.82] bg-white/[0.58] px-3 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.72),0_18px_68px_-52px_rgba(15,23,42,0.58)] backdrop-blur-xl dark:border-[#d6aa56]/26 dark:bg-[#0f0d09]/76 dark:shadow-[inset_0_1px_0_rgba(255,224,166,0.12),0_18px_68px_-52px_rgba(0,0,0,0.90)] sm:gap-2.5 sm:rounded-[20px] sm:px-4 sm:py-4">
          <div className="mx-auto flex max-w-[680px] flex-col items-center">
            <div className="mb-1 inline-flex items-center gap-1.5 rounded-full border border-[#dfe7f2] bg-white/[0.82] px-2 py-0.5 text-[10px] font-bold text-[#45515e] shadow-sm dark:border-[#d6aa56]/28 dark:bg-[#21180d] dark:text-[#d6aa56]">
              <Sparkles className="size-3 text-[#1456f0]" />
              1818 AI 商业图片
            </div>
            <h1 className="font-display text-[1.48rem] leading-[1] font-black tracking-[-0.06em] text-[#0b1020] dark:text-[#f5e6bf] sm:text-[2.35rem]">
              一键生成高质量<span className="text-[#1f43ee]">商业图片</span>
            </h1>
            <p className="mx-auto mt-1 max-w-[560px] text-[11px] leading-4 text-[#45515e] dark:text-[#b49a62] sm:mt-1.5 sm:text-sm sm:leading-5">
              商品海报、电商主图、社媒配图和参考图编辑都在同一个创作台完成，输入描述即可开始。
            </p>
          </div>
          <div className="mx-auto flex flex-wrap justify-center gap-1 text-[10px] font-semibold text-[#1f2937] sm:text-[11px]">
            {["商业海报", "电商主图", "社媒配图", "AI 修图", "参考图编辑"].map((item) => (
              <span key={item} className="rounded-[10px] border border-[#dfe7f2] bg-white/[0.78] px-2 py-0.5 shadow-sm dark:border-[#d6aa56]/22 dark:bg-[#21180d] dark:text-[#d6aa56]">
                {item}
              </span>
            ))}
          </div>
          {!showPromptPresets && (promptPresets.length > 0 || isLoadingPromptPresets) ? (
            <button
              type="button"
              className="mx-auto inline-flex items-center justify-center rounded-full border border-[#dfe7f2] bg-white/82 px-3 py-1.5 text-[11px] font-bold text-[#45515e] shadow-sm transition hover:border-[#bfdbfe] hover:text-[#1456f0] dark:border-[#d6aa56]/24 dark:bg-[#21180d] dark:text-[#d6aa56]"
              onClick={() => setShowPromptPresets(true)}
            >
              {isLoadingPromptPresets ? "预设整理中" : `查看 ${promptPresets.length} 个公开预设`}
            </button>
          ) : isLoadingPromptPresets ? (
            <div className="mx-auto flex min-h-[96px] w-full max-w-[520px] items-center justify-center gap-2 rounded-[16px] border border-dashed border-[#dfe7f2] bg-white/70 px-4 py-5 text-xs font-semibold text-[#45515e] dark:border-[#d6aa56]/22 dark:bg-[#15120d] dark:text-[#d6aa56]">
              <LoaderCircle className="size-4 animate-spin" />
              正在整理公开图库预设...
            </div>
          ) : promptPresets.length > 0 ? (
            <>
              <div className="hide-scrollbar flex gap-1.5 overflow-x-auto px-1 pb-1 text-left sm:grid sm:grid-cols-2 sm:overflow-visible lg:grid-cols-4">
                {visiblePromptPresets.map((preset) => (
                  <button
                    key={preset.id}
                    type="button"
                    className="group w-[164px] shrink-0 overflow-hidden rounded-[15px] border border-[#edf1f7] bg-white/95 transition hover:-translate-y-0.5 hover:shadow-[0_14px_26px_-20px_rgba(21,36,76,0.45)] dark:border-[#d6aa56]/22 dark:bg-[#15120d] dark:hover:shadow-[0_16px_34px_-26px_rgba(214,170,86,0.44)] sm:w-auto"
                    onClick={() => void onApplyPromptPreset(preset)}
                    aria-label={`套用预设：${preset.title}`}
                  >
                    <div className="relative aspect-[16/9] overflow-hidden bg-[#f0f0f0]">
                      <img
                        src={preset.imageSrc}
                        alt={preset.title}
                        loading="lazy"
                        className="h-full w-full object-cover transition duration-300 group-hover:scale-[1.03]"
                      />
                      <div className="absolute inset-x-0 bottom-0 flex items-center justify-between gap-1.5 bg-gradient-to-t from-black/70 via-black/25 to-transparent px-2 pt-7 pb-1.5">
                        <span className="rounded-full bg-white/92 px-1.5 py-0.5 text-[9px] font-medium text-[#18181b] shadow-sm">
                          {preset.size || "Auto"}
                        </span>
                        <span className="rounded-full bg-white/18 px-1.5 py-0.5 text-[9px] font-medium text-white shadow-sm backdrop-blur">
                          {preset.count} 张
                        </span>
                      </div>
                    </div>
                    <div className="flex flex-col gap-0.5 px-2.25 py-2">
                      <div className="font-display truncate text-[11px] font-bold text-[#222222] dark:text-[#f5e6bf]">{preset.title}</div>
                      <div className="line-clamp-1 text-[10px] leading-4 text-[#45515e] dark:text-[#9b875c]">{preset.hint}</div>
                      <div className="border-t border-[#f2f3f5] pt-0.5 text-[10px] font-bold text-[#1456f0] dark:border-[#f2f3f5] dark:text-[#1456f0]">套用预设</div>
                    </div>
                  </button>
                ))}
              </div>
              {canTogglePromptPresets ? (
                <button
                  type="button"
                  className="mx-auto rounded-full border border-[#dfe7f2] bg-white/82 px-3 py-1 text-[11px] font-bold text-[#45515e] shadow-sm transition hover:border-[#bfdbfe] hover:text-[#1456f0] dark:border-[#d6aa56]/24 dark:bg-[#21180d] dark:text-[#d6aa56]"
                  onClick={() => setShowAllPromptPresets((open) => !open)}
                >
                  {showAllPromptPresets ? "收起预设" : `查看更多预设（${hiddenPromptPresetCount}）`}
                </button>
              ) : null}
            </>
          ) : (
            <div className="mx-auto w-full max-w-[560px] rounded-[16px] border border-dashed border-[#dfe7f2] bg-white/64 px-4 py-4 text-xs leading-5 text-[#45515e] dark:border-[#d6aa56]/22 dark:bg-[#15120d] dark:text-[#b49a62]">
              {promptPresetStatus || "公开预设稍后加载。可以直接输入提示词开始，或打开市场复用现成方案。"}
            </div>
          )}
        </div>
      </div>
    );
  }

  const isExpandedConversation = expandedConversationId === selectedConversation.id;
  const initialVisibleTurnCount = getInitialVisibleTurnCount();
  const hiddenTurnCount = Math.max(0, selectedConversation.turns.length - initialVisibleTurnCount);
  const visibleTurns =
    hiddenTurnCount > 0 && !isExpandedConversation
      ? selectedConversation.turns.slice(-initialVisibleTurnCount)
      : selectedConversation.turns;
  const visibleTurnStartIndex = selectedConversation.turns.length - visibleTurns.length;

  return (
    <div className="mx-auto flex w-full max-w-[960px] flex-col gap-4 sm:gap-6">
      {hiddenTurnCount > 0 && !isExpandedConversation ? (
        <button
          type="button"
          className="mx-auto inline-flex items-center justify-center rounded-full border border-[#dfe7f2] bg-white/78 px-4 py-2 text-xs font-bold text-[#45515e] shadow-sm backdrop-blur transition hover:-translate-y-0.5 hover:bg-white dark:border-[#d6aa56]/24 dark:bg-[#15120d]/80 dark:text-[#d6aa56] dark:hover:bg-[#21180d]"
          onClick={() => setExpandedConversationId(selectedConversation.id)}
        >
          显示更早 {hiddenTurnCount} 轮
        </button>
      ) : null}
      {visibleTurns.map((turn, visibleTurnIndex) => {
        const turnIndex = visibleTurnStartIndex + visibleTurnIndex;
        const progress = progressByTurnKey[turnProgressKey(selectedConversation.id, turn.id)];
        const referenceLightboxImages = turn.referenceImages.map((image, index) => ({
          id: `${turn.id}-reference-${index}`,
          src: image.dataUrl,
        }));
        const downloadableImages = turn.images.flatMap((image, index) => {
          const src = image.status === "success" ? getStoredImageSrc(image) : "";
          return src
            ? [
                {
                  id: image.id,
                  selectionKey: imageSelectionKey(selectedConversation.id, turn.id, image.id),
                  src,
                  fileName: buildDownloadName(turn.createdAt, turn.id, index, image.outputFormat || turn.outputFormat),
                  imageIndex: index,
                },
              ]
            : [];
        });
        const selectedDownloadableImages = downloadableImages.filter((image) => selectedImageIds[image.selectionKey]);
        const successfulTurnImages = turn.images.flatMap((image) => {
          const src = image.status === "success" ? getStoredImageSrc(image) : "";
          return src
            ? [
                {
                  id: image.id,
                  src,
                  sizeLabel: image.b64_json ? formatBase64ImageFileSize(image.b64_json) : imageSizeLabels[image.id],
                  dimensions: imageDimensions[image.id],
                },
              ]
            : [];
        });
        const textReplyImages = turn.images
          .map((image, index) => ({ image, index }))
          .filter(({ image }) => image.status === "message" && Boolean(image.text_response));
        const visualImages = turn.images
          .map((image, index) => ({ image, index }))
          .filter(({ image }) => !textReplyImages.some((reply) => reply.image.id === image.id));
        const turnBusy = isTurnBusy(turn);
        const successCount = visualImages.filter(({ image }) => image.status === "success").length;
        const failedCount = visualImages.filter(({ image }) => image.status === "error").length;
        const cancelledCount = visualImages.filter(({ image }) => image.status === "cancelled").length;
        const resultCount = visualImages.length || (turnBusy ? turn.count : 0);
        const outcomeLabel = getTurnOutcomeLabel(successCount, failedCount, cancelledCount);
        const showResultSummary = turn.mode !== "chat" && (visualImages.length > 0 || turnBusy);
        const resultSizeLabel = getTurnResultSizeLabel(turn, imageDimensions);
        const progressStartedAt =
          progress && Number.isFinite(progress.startedAt) ? progress.startedAt : null;
        const elapsedClock = turnBusy
          ? progressStartedAt === null
            ? ""
            : formatElapsedClock(Math.max(0, Math.floor((progressNow - progressStartedAt) / 1000)))
          : "";
        const progressMessage =
          progress?.message || (turn.status === "queued" ? "等待前序任务" : turnBusy ? "正在处理图片" : "");
        const downloadActions =
          downloadableImages.length > 0 ? (
            <>
              <Button
                type="button"
                size="sm"
                className="h-8 rounded-full bg-[#1456f0] px-2.5 text-[11px] text-white shadow-sm hover:bg-[#2563eb] dark:bg-[#1456f0] dark:text-white dark:hover:bg-[#2563eb]"
                disabled={selectedDownloadableImages.length === 0 || downloadingKey !== null}
                onClick={() =>
                  void downloadItems(
                    `selected:${selectedConversation.id}:${turn.id}`,
                    selectedDownloadableImages,
                  )
                }
              >
                {downloadingKey === `selected:${selectedConversation.id}:${turn.id}` ? (
                  <LoaderCircle className="size-3 animate-spin" />
                ) : (
                  <Download className="size-3" />
                )}
                下载已选 ({selectedDownloadableImages.length})
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 rounded-full border-[#e5e7eb] bg-white px-2.5 text-[11px] text-[#45515e] shadow-sm hover:bg-black/[0.05] dark:border-[#d6aa56]/24 dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d]"
                disabled={downloadingKey !== null}
                onClick={() =>
                  void downloadItems(
                    `all:${selectedConversation.id}:${turn.id}`,
                    downloadableImages,
                  )
                }
              >
                {downloadingKey === `all:${selectedConversation.id}:${turn.id}` ? (
                  <LoaderCircle className="size-3 animate-spin" />
                ) : (
                  <Download className="size-3" />
                )}
                下载全部
              </Button>
            </>
          ) : null;

        return (
          <div key={turn.id} className="flex flex-col gap-3 sm:gap-4">
            <div className="flex justify-end">
              <article className="w-full max-w-[min(94%,760px)] rounded-[24px] border border-[#f2f3f5] bg-white px-4 py-3 text-left text-[14px] leading-6 text-[#222222] shadow-[0_4px_6px_rgba(0,0,0,0.08)] dark:border-[#d6aa56]/30 dark:bg-[#10100d]/94 dark:text-[#f5e6bf] dark:shadow-[0_22px_62px_-42px_rgba(0,0,0,0.96),inset_0_1px_0_rgba(255,224,166,0.10)] sm:px-5 sm:py-4 sm:text-[15px] sm:leading-7">
                <div className="mb-3 flex items-start justify-between gap-3 border-b border-[#f2f3f5] pb-2 dark:border-[#d6aa56]/16">
                  <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[11px] leading-5 text-[#45515e]">
                    <span className="rounded-full bg-[#f0f0f0] px-2.5 py-0.5 text-[#45515e] dark:bg-[#21180d] dark:text-[#d6aa56]">第 {turnIndex + 1} 轮</span>
                    <span className="rounded-full bg-[#f0f0f0] px-2.5 py-0.5 text-[#45515e] dark:bg-[#21180d] dark:text-[#d6aa56]">{getTurnModeLabel(turn)}</span>
                    <span className="rounded-full bg-[#f0f0f0] px-2.5 py-0.5 text-[#45515e] dark:bg-[#21180d] dark:text-[#d6aa56]">{turn.model}</span>
                    <span className="rounded-full bg-[#f0f0f0] px-2.5 py-0.5 text-[#45515e] dark:bg-[#21180d] dark:text-[#d6aa56]">
                      {getTurnStatusLabel(turn.status)}
                    </span>
                    <span className="px-1 text-[#8e8e93] dark:text-[#8f7b52]">{formatConversationTime(turn.createdAt)}</span>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    {turnBusy ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-8 rounded-full border-amber-200 bg-amber-50 text-amber-700 shadow-none hover:bg-amber-100 dark:border-[#d6aa56]/35 dark:bg-[#21180d] dark:text-[#ffcf73] dark:hover:bg-[#2a1f10]"
                        onClick={() => void onCancelTurn(selectedConversation.id, turn.id)}
                        aria-label="终止生成任务"
                        title="终止"
                      >
                        <CircleStop className="size-4" />
                      </Button>
                    ) : (
                      <>
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="size-8 rounded-full border-[#e5e7eb] bg-white text-[#45515e] shadow-none hover:bg-black/[0.05] dark:border-[#d6aa56]/24 dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                          onClick={() => onEditTurn(selectedConversation.id, turn.id)}
                          aria-label="编辑生成设置"
                          title="编辑"
                        >
                          <PencilLine className="size-4" />
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="size-8 rounded-full border-[#e5e7eb] bg-white text-[#45515e] shadow-none hover:bg-black/[0.05] dark:border-[#d6aa56]/24 dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                          disabled={turnBusy || !turn.prompt.trim()}
                          onClick={() => void onRegenerateTurn(selectedConversation.id, turn.id)}
                          aria-label="重新生成"
                          title="重新生成"
                        >
                          <RotateCcw className="size-4" />
                        </Button>
                      </>
                    )}
                  </div>
                </div>
                <div>
                  <div className="whitespace-pre-wrap break-words">{turn.prompt}</div>
                  {turn.referenceImages.length > 0 ? (
                    <div className="mt-3 flex flex-wrap justify-start gap-2">
                      {turn.referenceImages.map((image, index) => (
                        <button
                          key={`${turn.id}-${image.name}-${index}`}
                          type="button"
                          onClick={() => onOpenLightbox(referenceLightboxImages, index)}
                          className="group relative size-20 shrink-0 overflow-hidden rounded-2xl border border-stone-200/80 bg-stone-100/60 text-left transition hover:border-stone-300 dark:border-[#d6aa56]/24 dark:bg-[#21180d]/70 dark:hover:border-[#d6aa56]/42 sm:size-24"
                          aria-label={`预览参考图 ${image.name || index + 1}`}
                        >
                          <img
                            src={image.dataUrl}
                            alt={image.name || `参考图 ${index + 1}`}
                            loading="lazy"
                            decoding="async"
                            className="absolute inset-0 h-full w-full object-cover transition duration-200 group-hover:scale-[1.02]"
                          />
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
              </article>
            </div>

            <div className="flex justify-start">
              <section className="w-full px-1">
                {showResultSummary ? (
                  <div className="mb-3 flex flex-wrap items-center justify-between gap-2 sm:mb-4">
                    <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-[#45515e] dark:text-[#b49a62] sm:gap-2 sm:text-xs">
                      <span className="font-medium text-[#222222] dark:text-[#f5e6bf]">生成结果</span>
                      <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">{resultCount} 张</span>
                      {turn.count !== resultCount ? (
                        <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">目标 {turn.count} 张</span>
                      ) : null}
                      {resultSizeLabel ? <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">{resultSizeLabel}</span> : null}
                      {turn.quality ? (
                        <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">Quality {turn.quality}</span>
                      ) : null}
                      {turn.outputFormat ? (
                        <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">{turn.outputFormat.toUpperCase()}</span>
                      ) : null}
                      {turn.outputCompression != null && turn.outputFormat && turn.outputFormat !== "png" ? (
                        <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">压缩 {turn.outputCompression}</span>
                      ) : null}
                      {outcomeLabel ? <span className="rounded-full bg-[#f0f0f0] px-3 py-1 dark:bg-[#21180d] dark:text-[#d6aa56]">{outcomeLabel}</span> : null}
                      <span className={cn("rounded-full px-3 py-1", getStatusChipClass(turn.status))}>
                        {getTurnStatusLabel(turn.status)}
                      </span>
                    </div>
                    {turnBusy || downloadActions ? (
                      <div className="flex flex-wrap items-center justify-end gap-2">
                        {turnBusy ? (
                          <span className="w-fit whitespace-nowrap rounded-full bg-amber-50 px-3 py-1 text-[11px] text-amber-700 dark:bg-[#21180d] dark:text-[#ffcf73] sm:text-xs">
                            {progressMessage}
                          </span>
                        ) : null}
                        {downloadActions}
                      </div>
                    ) : null}
                  </div>
                ) : null}

                {textReplyImages.length > 0 ? (
                  <div className="mb-3 flex flex-col gap-2">
                    {textReplyImages.map(({ image, index }) => (
                      <div
                        key={image.id}
                        className="w-full max-w-[min(94%,760px)] rounded-[20px] border border-[#f2f3f5] bg-white px-4 py-3 text-left text-sm leading-6 text-[#45515e] shadow-[0_4px_6px_rgba(0,0,0,0.08)] dark:border-[#d6aa56]/28 dark:bg-[#10100d]/92 dark:text-[#f5e6bf] dark:shadow-[0_18px_54px_-42px_rgba(0,0,0,0.95)]"
                      >
                        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-1.5 text-[11px] text-stone-500 dark:text-[#9b875c]">
                            <span className="rounded-full bg-stone-100 px-2.5 py-0.5 text-stone-600 dark:bg-[#21180d] dark:text-[#d6aa56]">
                              {turn.mode === "chat" ? "对话回复" : "模型文本回复"}
                            </span>
                          </div>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-8 rounded-full border-[#e5e7eb] bg-white px-3 text-xs text-[#45515e] shadow-none hover:bg-black/[0.05] hover:text-[#18181b] dark:border-[#d6aa56]/24 dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                            disabled={turnBusy || !turn.prompt.trim()}
                            onClick={() => void onRetryImage(selectedConversation.id, turn.id, index)}
                          >
                            <RotateCcw className="size-3.5" />
                            {turn.mode === "chat" ? "重新发送" : "重试生成"}
                          </Button>
                        </div>
                        <div className="whitespace-pre-wrap break-words">{image.text_response}</div>
                      </div>
                    ))}
                  </div>
                ) : null}

                {visualImages.length > 0 ? (
                  <div className="columns-1 gap-3 sm:columns-2 sm:gap-4 xl:columns-3">
                    {visualImages.map(({ image, index }) => {
                    const imageSrc = image.status === "success" ? getStoredImageSrc(image) : "";
                    if (image.status === "success" && imageSrc) {
                      const currentIndex = successfulTurnImages.findIndex((item) => item.id === image.id);
                      const selectionKey = imageSelectionKey(selectedConversation.id, turn.id, image.id);
                      const selected = Boolean(selectedImageIds[selectionKey]);
                      const sizeLabel = image.b64_json ? formatBase64ImageFileSize(image.b64_json) : imageSizeLabels[image.id] || "";
                      const dimensions = imageResolutionLabel(image, imageDimensions[image.id]);
                      const imageMeta = [dimensions, sizeLabel].filter(Boolean).join(" | ");
                      const formatLabel = getImageFormatLabel(image, imageSrc);
                      const visibility = image.visibility || turn.visibility || "private";
                      const nextVisibility = visibility === "public" ? "private" : "public";
                      const visibilityMutatingKey = `${selectedConversation.id}:${turn.id}:${image.id}`;
                      const isVisibilityMutating = visibilityMutatingImageKey === visibilityMutatingKey;
                      const canUpdateVisibility = Boolean(image.path || image.url);
                      const shareKey = `${turn.id}:${image.id}`;

                      return (
                        <figure
                          key={image.id}
                          className={cn(
                            "group relative mb-3 inline-block w-full break-inside-avoid overflow-hidden rounded-[22px] bg-[#f0f0f0] shadow-[0_0_15px_rgba(44,30,116,0.16)] dark:bg-[#15120d] dark:shadow-[0_16px_44px_-32px_rgba(0,0,0,0.95),0_0_0_1px_rgba(214,170,86,0.12)] sm:mb-4",
                            selected && "ring-2 ring-[#1456f0]/90 ring-offset-2 dark:ring-[#d6aa56]/90 dark:ring-offset-[#090806]",
                          )}
                          onMouseLeave={(event) => blurFocusedElementInContainer(event.currentTarget)}
                        >
                          <button
                            type="button"
                            onClick={(event) => {
                              toggleImageSelection(selectionKey);
                              event.currentTarget.blur();
                            }}
                            className="block w-full cursor-pointer overflow-hidden text-left"
                            aria-label={selected ? "取消选择图片" : "选择图片"}
                          >
                            <img
                              src={imageSrc}
                              alt={`Generated result ${index + 1}`}
                              loading={visibleTurnIndex === visibleTurns.length - 1 && index === 0 ? "eager" : "lazy"}
                              decoding="async"
                              className="block h-auto w-full transition duration-200 group-hover:brightness-95"
                              onLoad={(event) => {
                                updateImageDimensions(
                                  image.id,
                                  event.currentTarget.naturalWidth,
                                  event.currentTarget.naturalHeight,
                                );
                                if (!image.b64_json) {
                                  ensureImageSizeLabel(image.id, imageSrc);
                                }
                              }}
                            />
                          </button>
                          <button
                            type="button"
                            onClick={(event) => {
                              toggleImageSelection(selectionKey);
                              event.currentTarget.blur();
                            }}
                            className={cn(
                              "absolute top-2 left-2 z-10 inline-flex size-6 items-center justify-center rounded-full border transition duration-150",
                              selected
                                ? "border-[#1456f0] bg-[#1456f0] text-white opacity-100 shadow-sm"
                                : "pointer-events-none border-white/90 bg-black/20 text-transparent opacity-0 shadow-sm group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 hover:bg-black/30",
                            )}
                            aria-label={selected ? "取消选择图片" : "选择图片"}
                          >
                            {selected ? <Check className="size-3.5" /> : null}
                          </button>
                          <div className="pointer-events-none absolute top-2 right-2 z-10 hidden items-center gap-1 opacity-0 transition duration-150 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 sm:flex">
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                void shareImage(turn, image, index, imageSrc);
                              }}
                              disabled={sharingImageKey !== null}
                              className="inline-flex h-7 items-center gap-1 rounded-full bg-white/95 px-2 text-[11px] font-medium text-stone-800 shadow-sm transition hover:bg-white hover:text-stone-950 disabled:cursor-not-allowed disabled:opacity-70 dark:bg-[#15120d]/95 dark:text-[#f5e6bf] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                              aria-label="分享图片"
                              title="分享"
                            >
                              {sharingImageKey === shareKey ? (
                                <LoaderCircle className="size-3 animate-spin" />
                              ) : (
                                <Share2 className="size-3" />
                              )}
                              分享
                            </button>
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                onOpenLightbox(successfulTurnImages, currentIndex);
                              }}
                              className="inline-flex h-7 items-center gap-1 rounded-full bg-white/95 px-2 text-[11px] font-medium text-stone-800 shadow-sm transition hover:bg-white hover:text-stone-950 dark:bg-[#15120d]/95 dark:text-[#f5e6bf] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                              aria-label="查看原图"
                              title="查看原图"
                            >
                              <Eye className="size-3" />
                              原图
                            </button>
                            <button
                              type="button"
                              onClick={(event) => {
                                event.currentTarget.blur();
                                onContinueEdit(selectedConversation.id, image);
                              }}
                              className="inline-flex size-7 items-center justify-center rounded-full bg-white/95 text-stone-800 shadow-sm transition hover:bg-white hover:text-stone-950 dark:bg-[#15120d]/95 dark:text-[#f5e6bf] dark:hover:bg-[#21180d] dark:hover:text-[#ffcf73]"
                              aria-label="加入编辑"
                              title="加入编辑"
                            >
                              <Plus className="size-3.5" />
                            </button>
                          </div>
                          <div className="absolute right-2 bottom-2 z-20 hidden items-center gap-1 sm:flex">
                            {canUpdateVisibility ? (
                              <button
                                type="button"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  event.currentTarget.blur();
                                  void onImageVisibilityChange(
                                    selectedConversation.id,
                                    turn.id,
                                    index,
                                    nextVisibility,
                                  );
                                }}
                                disabled={isVisibilityMutating}
                                className={cn(
                                  "inline-flex h-7 items-center gap-1.5 rounded-full px-2.5 text-[11px] font-medium opacity-0 shadow-sm transition group-hover:opacity-100 group-focus-within:opacity-100 disabled:cursor-not-allowed disabled:opacity-70",
                                  imageVisibilityActionClass(visibility),
                                )}
                                aria-label={visibility === "public" ? "取消公开图片" : "公开图片"}
                                title={visibility === "public" ? "取消公开" : "公开"}
                              >
                                {isVisibilityMutating ? (
                                  <LoaderCircle className="size-3 animate-spin" />
                                ) : visibility === "public" ? (
                                  <Lock className="size-3" />
                                ) : (
                                  <Globe2 className="size-3" />
                                )}
                                {visibility === "public" ? "取消公开" : "公开"}
                              </button>
                            ) : null}
                            <div
                              className={cn(
                                "pointer-events-none inline-flex h-7 items-center gap-1 rounded-full px-2 text-[11px] font-medium shadow-sm backdrop-blur-sm",
                                imageVisibilityPillClass(visibility),
                              )}
                            >
                              {visibility === "public" ? <Globe2 className="size-3" /> : <Lock className="size-3" />}
                              {imageVisibilityLabel(visibility)}
                            </div>
                          </div>
                          <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/55 via-black/20 to-transparent px-2.5 pt-8 pb-11 opacity-0 transition duration-150 group-hover:opacity-100 group-focus-within:opacity-100">
                            <div className="text-left text-white drop-shadow-sm">
                              <div className="text-[10px] font-bold tracking-wide">{formatLabel}</div>
                              {imageMeta ? (
                                <div className="mt-0.5 truncate text-[11px] text-white/90">{imageMeta}</div>
                              ) : null}
                            </div>
                          </div>
                          <div className="grid grid-cols-2 gap-1.5 border-t border-black/5 bg-white/96 p-1.5 shadow-[0_-10px_24px_rgba(15,23,42,0.10)] backdrop-blur dark:border-[#d6aa56]/16 dark:bg-[#0d0c09]/96 dark:shadow-[0_-12px_28px_rgba(0,0,0,0.72)] sm:hidden">
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                void shareImage(turn, image, index, imageSrc);
                              }}
                              disabled={sharingImageKey !== null}
                              className="inline-flex h-9 items-center justify-center gap-1 rounded-[12px] bg-[#edf4ff] px-1.5 text-[11px] font-semibold text-[#1456f0] transition hover:bg-[#dbeafe] active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-70 dark:bg-[#21180d] dark:text-[#ffcf73] dark:hover:bg-[#2a1f10]"
                              aria-label="分享图片"
                              title="分享"
                            >
                              {sharingImageKey === shareKey ? (
                                <LoaderCircle className="size-3.5 animate-spin" />
                              ) : (
                                <Share2 className="size-3.5" />
                              )}
                              分享
                            </button>
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                onContinueEdit(selectedConversation.id, image);
                              }}
                              className="inline-flex h-9 items-center justify-center gap-1 rounded-[12px] bg-[#f8fafc] px-1.5 text-[11px] font-semibold text-[#334155] transition hover:bg-[#eef2f7] active:scale-[0.98] dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d]"
                              aria-label="加入参考图"
                              title="加入参考图"
                            >
                              <Images className="size-3.5" />
                              参考图
                            </button>
                            <button
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation();
                                event.currentTarget.blur();
                                onOpenLightbox(successfulTurnImages, currentIndex);
                              }}
                              className="inline-flex h-9 items-center justify-center gap-1 rounded-[12px] bg-[#181e25] px-1.5 text-[11px] font-semibold text-white transition hover:bg-[#2a323d] active:scale-[0.98] dark:bg-[#d6aa56] dark:text-[#100b04] dark:hover:bg-[#e7bf6d]"
                              aria-label="查看原图"
                              title="查看原图"
                            >
                              <Eye className="size-3.5" />
                              原图
                            </button>
                            {canUpdateVisibility ? (
                              <button
                                type="button"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  event.currentTarget.blur();
                                  void onImageVisibilityChange(
                                    selectedConversation.id,
                                    turn.id,
                                    index,
                                    nextVisibility,
                                  );
                                }}
                                disabled={isVisibilityMutating}
                                className={cn(
                                  "inline-flex h-9 items-center justify-center gap-1 rounded-[12px] px-1.5 text-[11px] font-semibold transition active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-70",
                                  visibility === "public"
                                    ? "bg-[#e8f2ff] text-[#1456f0] hover:bg-[#dbeafe] dark:bg-[#21180d] dark:text-[#ffcf73] dark:hover:bg-[#2a1f10]"
                                    : "bg-[#f1f5f9] text-[#334155] hover:bg-[#e2e8f0] dark:bg-[#15120d] dark:text-[#d6aa56] dark:hover:bg-[#21180d]",
                                )}
                                aria-label={visibility === "public" ? "取消公开图片" : "公开图片"}
                                title={visibility === "public" ? "取消公开" : "公开图片"}
                              >
                                {isVisibilityMutating ? (
                                  <LoaderCircle className="size-3.5 animate-spin" />
                                ) : visibility === "public" ? (
                                  <Globe2 className="size-3.5" />
                                ) : (
                                  <Lock className="size-3.5" />
                                )}
                                {visibility === "public" ? "取消公开" : "公开"}
                              </button>
                            ) : (
                              <span
                                className={cn(
                                  "inline-flex h-9 items-center justify-center gap-1 rounded-[12px] px-1.5 text-[11px] font-semibold",
                                  visibility === "public"
                                    ? "bg-[#e8f2ff] text-[#1456f0] dark:bg-[#21180d] dark:text-[#ffcf73]"
                                    : "bg-[#f1f5f9] text-[#334155] dark:bg-[#15120d] dark:text-[#d6aa56]",
                                )}
                              >
                                {visibility === "public" ? <Globe2 className="size-3.5" /> : <Lock className="size-3.5" />}
                                {visibility === "public" ? "公开" : "私有"}
                              </span>
                            )}
                          </div>
                        </figure>
                      );
                    }

                    if (image.status === "cancelled") {
                      return (
                        <div
                          key={image.id}
                          className="mb-3 inline-block min-h-[104px] w-full break-inside-avoid overflow-hidden rounded-[18px] border border-amber-200 bg-amber-50 dark:border-[#d6aa56]/30 dark:bg-[#21180d]/76 sm:mb-4 sm:min-h-[160px]"
                        >
                          <div className="flex min-h-[104px] items-center justify-center px-4 py-4 text-center text-sm leading-6 text-amber-700 dark:text-[#ffcf73] sm:min-h-[160px] sm:px-6 sm:py-8">
                            <span className="line-clamp-3">{image.error || "任务已终止"}</span>
                          </div>
                        </div>
                      );
                    }

                    if (image.status === "error") {
                      return (
                        <div
                          key={image.id}
                          className="mb-3 inline-flex max-h-[190px] min-h-[128px] w-full break-inside-avoid flex-col overflow-hidden rounded-[18px] border border-rose-200 bg-rose-50 dark:border-rose-300/28 dark:bg-[#1f0f0c]/76 sm:mb-4 sm:min-h-[160px]"
                        >
                          <div className="min-h-0 flex-1 overflow-y-auto whitespace-pre-line px-4 py-3 text-center text-sm leading-6 text-rose-600 dark:text-rose-200 sm:flex sm:items-center sm:justify-center sm:px-5">
                            {image.error || "生成失败"}
                          </div>
                          <div className="flex justify-end border-t border-rose-100 bg-white/70 px-3 py-2.5 dark:border-rose-300/15 dark:bg-black/10">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="h-8 rounded-full border-rose-200 bg-white px-3 text-xs text-rose-600 shadow-none hover:bg-rose-50 hover:text-rose-700 dark:border-rose-300/28 dark:bg-[#2a1510] dark:text-rose-200 dark:hover:bg-[#361a14] dark:hover:text-rose-100"
                              disabled={turnBusy || !turn.prompt.trim()}
                              onClick={() => void onRetryImage(selectedConversation.id, turn.id, index)}
                            >
                              <RotateCcw className="size-3.5" />
                              重试
                            </Button>
                          </div>
                        </div>
                      );
                    }

                    return (
                      <div
                        key={image.id}
                        className="mb-3 inline-block min-h-[118px] w-full break-inside-avoid overflow-hidden rounded-[18px] border border-stone-200/80 bg-stone-100/80 dark:border-[#d6aa56]/24 dark:bg-[#15120d]/86 sm:mb-4 sm:min-h-[160px]"
                      >
                        <div className="flex min-h-[118px] flex-col items-center justify-center gap-2 px-4 py-4 text-center text-stone-500 dark:text-[#b49a62] sm:min-h-[160px] sm:px-5 sm:py-5">
                          <div className="rounded-full bg-white p-2.5 shadow-sm dark:bg-[#21180d] dark:text-[#ffcf73] dark:shadow-none sm:p-3">
                            {turn.status === "queued" ? (
                              <Clock3 className="size-4.5 sm:size-5" />
                            ) : (
                              <LoaderCircle className="size-4.5 animate-spin sm:size-5" />
                            )}
                          </div>
                          <p className="text-xs font-medium sm:text-sm">
                            {turn.mode === "chat"
                              ? turn.status === "queued"
                                ? "已加入当前对话队列..."
                                : "正在等待回复..."
                              : turn.status === "queued"
                                ? "已加入当前对话队列..."
                                : "正在处理图片..."}
                          </p>
                          {elapsedClock ? (
                            <p className="min-w-[7rem] rounded-full bg-white/70 px-2.5 py-1 font-mono text-[11px] tabular-nums text-stone-400 dark:bg-[#090806]/72 dark:text-[#9b875c] sm:min-w-[7.5rem] sm:text-xs">
                              已等待 {elapsedClock}
                            </p>
                          ) : null}
                        </div>
                      </div>
                    );
                    })}
                  </div>
                ) : null}

              </section>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function getTurnStatusLabel(status: ImageTurnStatus) {
  if (status === "queued") {
    return "排队中";
  }
  if (status === "generating") {
    return "处理中";
  }
  if (status === "success") {
    return "已完成";
  }
  if (status === "message") {
    return "文本回复";
  }
  if (status === "cancelled") {
    return "已终止";
  }
  return "失败";
}

function turnProgressKey(conversationId: string, turnId: string) {
  return `${conversationId}:${turnId}`;
}

function formatElapsedClock(totalSeconds: number) {
  const safeSeconds = Math.max(0, totalSeconds);
  const hours = Math.floor(safeSeconds / 3600);
  const minutes = Math.floor((safeSeconds % 3600) / 60);
  const seconds = safeSeconds % 60;
  if (hours > 0) {
    return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function getStatusChipClass(status: ImageTurnStatus) {
  if (status === "queued") {
    return "bg-amber-50 text-amber-700 dark:bg-[#21180d] dark:text-[#ffcf73]";
  }
  if (status === "generating") {
    return "bg-blue-50 text-[#1456f0] dark:bg-[#21180d] dark:text-[#ffcf73]";
  }
  if (status === "success") {
    return "bg-emerald-50 text-emerald-700 dark:bg-[#1c2515] dark:text-[#c7d88a]";
  }
  if (status === "message") {
    return "bg-stone-100 text-stone-600 dark:bg-[#21180d] dark:text-[#d6aa56]";
  }
  if (status === "cancelled") {
    return "bg-amber-50 text-amber-700 dark:bg-[#21180d] dark:text-[#ffcf73]";
  }
  return "bg-rose-50 text-rose-700 dark:bg-[#2a1510] dark:text-rose-200";
}

function getTurnOutcomeLabel(successCount: number, failedCount: number, cancelledCount: number) {
  if (failedCount === 0 && cancelledCount === 0) {
    return "";
  }
  const parts = [`成功 ${successCount}`];
  if (failedCount > 0) {
    parts.push(`失败 ${failedCount}`);
  }
  if (cancelledCount > 0) {
    parts.push(`终止 ${cancelledCount}`);
  }
  return parts.join(" / ");
}

function getTurnModeLabel(turn: ImageTurn) {
  if (turn.mode === "chat") {
    return "对话";
  }
  if (turn.mode === "generate") {
    return "文生图";
  }
  if (turn.mode === "edit" && turn.referenceImages.some((image) => image.source === "conversation")) {
    return "编辑图";
  }
  return "图生图";
}

function formatImageDimensions(width: number, height: number) {
  return `${width} x ${height}`;
}
