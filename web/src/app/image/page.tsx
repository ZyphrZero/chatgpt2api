"use client";

import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { History, LoaderCircle, Plus, Trash2 } from "lucide-react";
import { useLocation } from "react-router-dom";
import { toast } from "sonner";

import { ImageComposer } from "@/app/image/components/image-composer";
import type { ImageLightboxItem } from "@/app/image/components/image-results";
import type { BananaPrompt } from "@/app/image/banana-prompts";
import {
  CUSTOM_IMAGE_ASPECT_RATIO,
  DEFAULT_IMAGE_CUSTOM_HEIGHT,
  DEFAULT_IMAGE_CUSTOM_RATIO,
  DEFAULT_IMAGE_CUSTOM_WIDTH,
  IMAGE_ASPECT_RATIO_OPTIONS,
  IMAGE_QUALITY_OPTIONS,
  IMAGE_RESOLUTION_OPTIONS,
  IMAGE_SIZE_MODE_OPTIONS,
  buildImageSize,
  formatImageSizeDisplay,
  getImageSizeSelectionFromSize,
  isImageAspectRatio,
  isImageResolution,
  isImageSizeMode,
  parseImageRatio,
  type ImageAspectRatio,
  type ImageResolution,
  type ImageSizeMode,
  type ImageSizeSelection,
} from "@/app/image/image-options";
import {
  imagePromptPresetFromPublicImage,
  publicImageOriginalPrompt,
  type ImagePromptPreset,
} from "@/app/image/image-presets";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  cancelCreationTask,
  CHAT_MODEL_OPTIONS,
  createChatCompletionTask,
  createImageEditTask,
  createImageGenerationTask,
  createResponseImageGenerationTask,
  DEFAULT_CHAT_MODEL,
  DEFAULT_IMAGE_MODEL,
  fetchCreationTasks,
  fetchManagedImages,
  IMAGE_CREATION_MODEL_OPTIONS,
  IMAGE_OUTPUT_FORMAT_OPTIONS,
  isChatModel,
  isImageCreationModel,
  isImageModel,
  isImageOutputFormat,
  isImageQuality,
  isImageTaskModel,
  isResponseImageToolModel,
  supportsImageQuality,
  updateManagedImageVisibility,
  type ImageModel,
  type ImageOutputFormat,
  type ImageQuality,
  type CreationTask,
  type CreationTaskMessage,
  type ImageVisibility,
  type ManagedImage,
} from "@/lib/api";
import { clearImageManagerCache } from "@/lib/image-manager-cache";
import { getManagedImagePathFromUrl } from "@/lib/image-path";
import { cn } from "@/lib/utils";
import { useAuthGuard } from "@/lib/use-auth-guard";
import {
  ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY,
  clearImageConversations,
  deleteImageConversation,
  getImageConversationStats,
  IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT,
  IMAGE_CONVERSATIONS_CHANGED_EVENT,
  listImageConversations,
  saveImageConversation,
  saveImageConversations,
  type ImageConversation,
  type ImageConversationMode,
  type ImageTurn,
  type ImageTurnStatus,
  type StoredImageSizeSelection,
  type StoredImage,
  type StoredReferenceImage,
} from "@/store/image-conversations";
import {
  clearImageTurnProgress,
  getImageTurnProgressSnapshot,
  imageTurnProgressKey,
  setImageTurnProgress,
  subscribeImageTurnProgress,
  type ImageTurnProgress,
} from "@/store/image-turn-progress";

const ImagePromptMarket = lazy(() =>
  import("@/app/image/components/image-prompt-market").then((module) => ({ default: module.ImagePromptMarket })),
);
const ImageResults = lazy(() =>
  import("@/app/image/components/image-results").then((module) => ({ default: module.ImageResults })),
);
const ImageSidebar = lazy(() =>
  import("@/app/image/components/image-sidebar").then((module) => ({ default: module.ImageSidebar })),
);
const ImageTurnEditDialog = lazy(() =>
  import("@/app/image/components/image-turn-edit-dialog").then((module) => ({ default: module.ImageTurnEditDialog })),
);
const ImageLightbox = lazy(() =>
  import("@/components/image-lightbox").then((module) => ({ default: module.ImageLightbox })),
);

const COMPOSER_MODE_STORAGE_KEY = "chatgpt2api:image_composer_mode";
const IMAGE_MODEL_STORAGE_KEY = "chatgpt2api:image_last_model";
const IMAGE_SIZE_STORAGE_KEY = "chatgpt2api:image_last_size";
const IMAGE_SIZE_MODE_STORAGE_KEY = "chatgpt2api:image_last_size_mode";
const IMAGE_ASPECT_RATIO_STORAGE_KEY = "chatgpt2api:image_last_aspect_ratio";
const IMAGE_RESOLUTION_STORAGE_KEY = "chatgpt2api:image_last_resolution";
const IMAGE_CUSTOM_RATIO_STORAGE_KEY = "chatgpt2api:image_last_custom_ratio";
const IMAGE_CUSTOM_WIDTH_STORAGE_KEY = "chatgpt2api:image_last_custom_width";
const IMAGE_CUSTOM_HEIGHT_STORAGE_KEY = "chatgpt2api:image_last_custom_height";
const IMAGE_QUALITY_STORAGE_KEY = "chatgpt2api:image_last_quality";
const IMAGE_OUTPUT_FORMAT_STORAGE_KEY = "chatgpt2api:image_last_output_format";
const IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY = "chatgpt2api:image_last_output_compression";
const IMAGE_SPEED_DEFAULTS_VERSION_STORAGE_KEY = "chatgpt2api:image_speed_defaults_version";
const IMAGE_SPEED_DEFAULTS_VERSION = "20260513-medium-webp";
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";
const DEFAULT_IMAGE_QUALITY: ImageQuality = "medium";
const DEFAULT_IMAGE_OUTPUT_FORMAT: ImageOutputFormat = "webp";
const activeConversationQueueIds = new Set<string>();
const EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE = "__empty_aspect_ratio__";
const MISSING_RECOVERABLE_TASK_ID_ERROR = "页面刷新或任务中断，未找到可恢复的任务 ID";
const MISSING_CREATION_TASK_ERROR = "页面刷新或服务重启后任务状态已丢失，请重新生成";
const CREATION_TASK_POLL_INTERVAL_MS = 500;
const CREATION_TASK_POLL_RETRY_LIMIT = 12;

type ComposerMode = "chat" | "image";

type EditingTurnDraft = {
  conversationId: string;
  turnId: string;
  prompt: string;
  model: ImageModel;
  mode: ImageConversationMode;
  count: string;
  sizeMode: ImageSizeMode;
  aspectRatio: ImageAspectRatio;
  resolution: ImageResolution;
  customRatio: string;
  customWidth: string;
  customHeight: string;
  quality: ImageQuality;
  outputFormat: ImageOutputFormat;
  outputCompression: string;
  visibility: ImageVisibility;
  referenceImages: StoredReferenceImage[];
};

type CreationTaskDataItem = NonNullable<CreationTask["data"]>[number];

function buildConversationTitle(prompt: string) {
  const trimmed = prompt.trim();
  if (trimmed.length <= 12) {
    return trimmed;
  }
  return `${trimmed.slice(0, 12)}...`;
}

function formatConversationTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function createId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(new Error("读取参考图失败"));
    reader.readAsDataURL(file);
  });
}

function dataUrlToFile(dataUrl: string, fileName: string, mimeType?: string) {
  const [header, content] = dataUrl.split(",", 2);
  const matchedMimeType = header.match(/data:(.*?);base64/)?.[1];
  const binary = atob(content || "");
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new File([bytes], fileName, { type: mimeType || matchedMimeType || "image/png" });
}

function buildReferenceImageFromResult(image: StoredImage, fileName: string): StoredReferenceImage | null {
  if (!image.b64_json) {
    return null;
  }

  return {
    name: fileName,
    type: "image/png",
    dataUrl: `data:image/png;base64,${image.b64_json}`,
  };
}

async function fetchImageAsFile(url: string, fileName: string) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error("读取结果图失败");
  }
  const blob = await response.blob();
  return new File([blob], fileName, { type: blob.type || "image/png" });
}

function buildReferenceFileName(url: string, index: number, fallbackPrefix: string) {
  const path = url.split(/[?#]/, 1)[0] || "";
  const rawName = path.split("/").filter(Boolean).pop() || "";
  let name = rawName;
  try {
    name = rawName ? decodeURIComponent(rawName) : "";
  } catch {
    name = rawName;
  }
  if (name) {
    return name.includes(".") ? name : `${name}.png`;
  }
  return `${fallbackPrefix}-${index + 1}.png`;
}

async function buildReferenceImageFromUrl(
  url: string,
  index: number,
  fallbackPrefix: string,
): Promise<StoredReferenceImage> {
  const file = await fetchImageAsFile(url, buildReferenceFileName(url, index, fallbackPrefix));
  return {
    name: file.name,
    type: file.type || "image/png",
    dataUrl: await readFileAsDataUrl(file),
    source: "upload",
  };
}

function getPromptReferenceImageUrls(prompt: BananaPrompt) {
  const urls = prompt.referenceImageUrls.length > 0 ? prompt.referenceImageUrls : [prompt.preview];
  return Array.from(new Set(urls.map((url) => url.trim()).filter(Boolean)));
}

function buildPublicGalleryPresets(items: ManagedImage[]) {
  return items
    .filter((item) => item.visibility === "public" && publicImageOriginalPrompt(item))
    .slice(0, 8)
    .map(imagePromptPresetFromPublicImage);
}

function isCompactImageViewport() {
  if (typeof window === "undefined") {
    return false;
  }
  return window.matchMedia?.("(max-width: 768px), (pointer: coarse)").matches ?? false;
}

function shouldLoadPublicPresetsOnStartup() {
  if (typeof window === "undefined") {
    return false;
  }
  return !isCompactImageViewport();
}

function imageProgressTickIntervalMs() {
  return isCompactImageViewport() ? 2500 : 1000;
}

function imageTaskPollingIntervalMs() {
  return isCompactImageViewport() ? 3500 : 2000;
}

function shouldRenderDesktopSidebar() {
  if (typeof window === "undefined") {
    return true;
  }
  return window.matchMedia?.("(min-width: 1024px)").matches ?? true;
}

function scheduleIdleStartupWork(callback: () => void) {
  if (typeof window === "undefined") {
    return () => undefined;
  }
  if ("requestIdleCallback" in window) {
    const idleId = window.requestIdleCallback(callback, { timeout: 1800 });
    return () => window.cancelIdleCallback(idleId);
  }
  const timer = globalThis.setTimeout(callback, 900);
  return () => globalThis.clearTimeout(timer);
}

function scheduleInitialHistoryLoad(callback: () => void) {
  if (typeof window === "undefined") {
    return () => undefined;
  }
  let cancelled = false;
  let idleId: number | null = null;
  let timer: ReturnType<typeof globalThis.setTimeout> | null = null;
  const frameId = window.requestAnimationFrame(() => {
    if (cancelled) {
      return;
    }
    if ("requestIdleCallback" in window) {
      idleId = window.requestIdleCallback(callback, { timeout: 500 });
      return;
    }
    timer = globalThis.setTimeout(callback, 120);
  });
  return () => {
    cancelled = true;
    window.cancelAnimationFrame(frameId);
    if (idleId !== null) {
      window.cancelIdleCallback(idleId);
    }
    if (timer !== null) {
      globalThis.clearTimeout(timer);
    }
  };
}

function scheduleImagePreferencePersist(callback: () => void) {
  if (typeof window === "undefined") {
    return () => undefined;
  }
  if ("requestIdleCallback" in window) {
    const idleId = window.requestIdleCallback(callback, { timeout: 700 });
    return () => window.cancelIdleCallback(idleId);
  }
  const timer = globalThis.setTimeout(callback, 250);
  return () => globalThis.clearTimeout(timer);
}

function ImageSidebarFallback({ compact = false }: { compact?: boolean }) {
  return (
    <div className={cn("h-full min-h-0 rounded-[18px] bg-white/45 p-2 dark:bg-[#15120d]/60", compact ? "min-h-[240px]" : "")}>
      <div className="mb-2 h-9 rounded-[16px] bg-black/[0.05] dark:bg-white/10" />
      <div className="space-y-2">
        {Array.from({ length: compact ? 4 : 6 }).map((_, index) => (
          <div key={index} className="h-14 rounded-[15px] bg-black/[0.035] dark:bg-white/[0.075]" />
        ))}
      </div>
    </div>
  );
}

function LazyDialogFallback({ label }: { label: string }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 px-4 backdrop-blur-[2px]">
      <div className="w-[min(92vw,420px)] rounded-[28px] border border-white/75 bg-white/92 p-5 text-center text-sm font-semibold text-[#45515e] shadow-[0_28px_80px_-36px_rgba(15,23,42,0.55)] dark:border-[#d6aa56]/24 dark:bg-[#0e0d0a]/92 dark:text-[#d6aa56]">
        {label}
      </div>
    </div>
  );
}

async function buildReferenceImageFromStoredImage(image: StoredImage, fileName: string) {
  const direct = buildReferenceImageFromResult(image, fileName);
  if (direct) {
    return {
      referenceImage: direct,
      file: dataUrlToFile(direct.dataUrl, direct.name, direct.type),
    };
  }

  if (!image.url) {
    return null;
  }
  const file = await fetchImageAsFile(image.url, fileName);
  return {
    referenceImage: {
      name: file.name,
      type: file.type || "image/png",
      dataUrl: await readFileAsDataUrl(file),
    },
    file,
  };
}

const IMAGE_TASK_IMAGE_COUNT = 1;

function normalizeRequestedImageCount(value: string | number) {
  return Math.max(1, Math.min(10, Number(value) || 1));
}

function isInvalidCustomRatioSelection(sizeMode: ImageSizeMode, aspectRatio: ImageAspectRatio, customRatio: string) {
  return sizeMode === "ratio" && aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO && !parseImageRatio(customRatio);
}

function positiveDimension(value: unknown) {
  const dimension = Number(value);
  return Number.isFinite(dimension) && dimension > 0 ? Math.round(dimension) : undefined;
}

function normalizeOutputCompressionValue(value: unknown): number | undefined {
  if (value === undefined || value === null || String(value).trim() === "") {
    return undefined;
  }
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric < 0) {
    return undefined;
  }
  return Math.min(100, Math.round(numeric));
}

function imageOutputCompressionForFormat(format: ImageOutputFormat, value: unknown) {
  if (format === "png") {
    return undefined;
  }
  return normalizeOutputCompressionValue(value);
}

function imageTaskBatchId(turnId: string, imageIndex: number) {
  return `${turnId}-task-${Math.floor(imageIndex / IMAGE_TASK_IMAGE_COUNT)}`;
}

function imageTaskIdForImage(turnId: string, images: StoredImage[], imageIndex: number) {
  return images[imageIndex]?.taskId || imageTaskBatchId(turnId, imageIndex);
}

function recoverableImageTaskId(turnId: string, imageIndex: number) {
  return imageTaskBatchId(`${turnId}-${createId()}`, imageIndex);
}

function imageDataIndexForTask(images: StoredImage[], imageIndex: number) {
  const taskId = images[imageIndex]?.taskId || images[imageIndex]?.id;
  if (!taskId) {
    return 0;
  }
  return images.slice(0, imageIndex + 1).filter((image) => (image.taskId || image.id) === taskId).length - 1;
}

const STORED_IMAGE_FIELDS: Array<keyof StoredImage> = [
  "id",
  "taskId",
  "status",
  "path",
  "visibility",
  "b64_json",
  "url",
  "width",
  "height",
  "resolution",
  "outputFormat",
  "revised_prompt",
  "error",
  "text_response",
];

function updateStoredImage(image: StoredImage, updates: Partial<StoredImage>): StoredImage {
  const next = { ...image, ...updates };
  return STORED_IMAGE_FIELDS.every((field) => image[field] === next[field]) ? image : next;
}

function recoverStoredImageForResubmit(image: StoredImage, turn: ImageTurn, imageIndex: number): StoredImage {
  return updateStoredImage(image, {
    taskId: image.taskId || recoverableImageTaskId(turn.id, imageIndex),
    status: "loading",
    b64_json: undefined,
    url: undefined,
    path: undefined,
    width: undefined,
    height: undefined,
    resolution: undefined,
    visibility: turn.mode === "chat" ? undefined : image.visibility || turn.visibility || "private",
    revised_prompt: undefined,
    text_response: undefined,
    error: undefined,
  });
}

function taskDataToStoredImage(image: StoredImage, task: CreationTask, dataIndex = 0, fallbackVisibility?: ImageVisibility): StoredImage {
  const taskVisibility = task.visibility || fallbackVisibility || image.visibility || "private";
  const successUpdates = (item: CreationTaskDataItem) => {
    const width = positiveDimension(item.width);
    const height = positiveDimension(item.height);
    return {
      taskId: task.id,
      status: "success" as const,
      b64_json: item.b64_json,
      url: item.url,
      path: item.url ? getManagedImagePathFromUrl(item.url) || image.path : image.path,
      visibility: taskVisibility,
      width,
      height,
      resolution: item.resolution || (width && height ? `${width}x${height}` : image.resolution),
      outputFormat: item.output_format || task.output_format || image.outputFormat,
      revised_prompt: item.revised_prompt,
      text_response: undefined,
      error: undefined,
    };
  };
  if (task.status === "success") {
    if (task.output_type === "text") {
      return updateStoredImage(image, {
        taskId: task.id,
        status: "message",
        text_response: task.data?.[dataIndex]?.text_response || task.error || "",
        b64_json: undefined,
        url: undefined,
        path: undefined,
        visibility: undefined,
        revised_prompt: undefined,
        error: undefined,
      });
    }
    const item = task.data?.[dataIndex];
    if (!item?.b64_json && !item?.url) {
      if (dataIndex > 0 && image.taskId !== image.id) {
        return updateStoredImage(image, {
          taskId: image.id,
          status: "loading",
          error: undefined,
        });
      }
      return updateStoredImage(image, {
        taskId: task.id,
        status: "error",
        error: `未返回第 ${dataIndex + 1} 张图片数据`,
      });
    }
    return updateStoredImage(image, successUpdates(item));
  }

  if (task.status === "error") {
    if (task.output_type === "text") {
      return updateStoredImage(image, {
        taskId: task.id,
        status: "message",
        text_response: task.error || "",
        b64_json: undefined,
        url: undefined,
        path: undefined,
        visibility: undefined,
        revised_prompt: undefined,
        error: undefined,
      });
    }
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, successUpdates(item));
    }
    return updateStoredImage(image, {
      taskId: task.id,
      status: "error",
      text_response: undefined,
      error: formatCreationTaskErrorMessage(task.error || "生成失败"),
    });
  }

  if (task.status === "cancelled") {
    const item = task.data?.[dataIndex];
    if (item?.b64_json || item?.url) {
      return updateStoredImage(image, successUpdates(item));
    }
    return updateStoredImage(image, {
      taskId: task.id,
      status: "cancelled",
      error: task.error || "任务已终止",
    });
  }

  return updateStoredImage(image, {
    taskId: task.id,
    status: "loading",
    text_response: undefined,
    error: undefined,
  });
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function pickFallbackConversationId(conversations: ImageConversation[]) {
  const activeConversation = conversations.find((conversation) =>
    conversation.turns.some((turn) => turn.status === "queued" || turn.status === "generating"),
  );
  return activeConversation?.id ?? conversations[0]?.id ?? null;
}

function sortImageConversations(conversations: ImageConversation[]) {
  return [...conversations].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

function getStoredImageModel(): ImageModel {
  if (typeof window === "undefined") {
    return DEFAULT_IMAGE_MODEL;
  }
  const storedModel = window.localStorage.getItem(IMAGE_MODEL_STORAGE_KEY);
  return isImageModel(storedModel) ? storedModel : DEFAULT_IMAGE_MODEL;
}

function getStoredComposerMode(): ComposerMode {
  if (typeof window === "undefined") {
    return "image";
  }
  return window.localStorage.getItem(COMPOSER_MODE_STORAGE_KEY) === "chat" ? "chat" : "image";
}

function getStoredImageQuality(): ImageQuality {
	if (typeof window === "undefined") {
		return DEFAULT_IMAGE_QUALITY;
	}
	if (window.localStorage.getItem(IMAGE_SPEED_DEFAULTS_VERSION_STORAGE_KEY) !== IMAGE_SPEED_DEFAULTS_VERSION) {
		return DEFAULT_IMAGE_QUALITY;
	}
	const storedQuality = window.localStorage.getItem(IMAGE_QUALITY_STORAGE_KEY);
	return isImageQuality(storedQuality) ? storedQuality : DEFAULT_IMAGE_QUALITY;
}

function getStoredImageSizeSelection(): ImageSizeSelection {
  if (typeof window === "undefined") {
    return getImageSizeSelectionFromSize("");
  }
  const fallbackSelection = getImageSizeSelectionFromSize(window.localStorage.getItem(IMAGE_SIZE_STORAGE_KEY) || "");
  const storedSizeMode = window.localStorage.getItem(IMAGE_SIZE_MODE_STORAGE_KEY);
  const storedAspectRatio = window.localStorage.getItem(IMAGE_ASPECT_RATIO_STORAGE_KEY) || "";
  const storedResolution = window.localStorage.getItem(IMAGE_RESOLUTION_STORAGE_KEY);
  const customRatio = window.localStorage.getItem(IMAGE_CUSTOM_RATIO_STORAGE_KEY) || fallbackSelection.customRatio;
  const customWidth = window.localStorage.getItem(IMAGE_CUSTOM_WIDTH_STORAGE_KEY) || fallbackSelection.customWidth;
  const customHeight = window.localStorage.getItem(IMAGE_CUSTOM_HEIGHT_STORAGE_KEY) || fallbackSelection.customHeight;
  if (isImageSizeMode(storedSizeMode) && isImageAspectRatio(storedAspectRatio) && isImageResolution(storedResolution)) {
    return {
      mode: storedSizeMode,
      aspectRatio: storedAspectRatio,
      resolution: storedResolution,
      customRatio,
      customWidth,
      customHeight,
    };
  }
  return fallbackSelection;
}

function getStoredImageOutputFormat(): ImageOutputFormat {
	if (typeof window === "undefined") {
		return DEFAULT_IMAGE_OUTPUT_FORMAT;
	}
	if (window.localStorage.getItem(IMAGE_SPEED_DEFAULTS_VERSION_STORAGE_KEY) !== IMAGE_SPEED_DEFAULTS_VERSION) {
		return DEFAULT_IMAGE_OUTPUT_FORMAT;
	}
	const storedFormat = window.localStorage.getItem(IMAGE_OUTPUT_FORMAT_STORAGE_KEY);
	return isImageOutputFormat(storedFormat) ? storedFormat : DEFAULT_IMAGE_OUTPUT_FORMAT;
}

function getStoredImageOutputCompression(): string {
  if (typeof window === "undefined") {
    return "";
  }
  const normalized = normalizeOutputCompressionValue(window.localStorage.getItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY));
  return normalized === undefined ? "" : String(normalized);
}

function getStoredImagePreferences() {
  return {
    model: getStoredImageModel(),
    sizeSelection: getStoredImageSizeSelection(),
    quality: getStoredImageQuality(),
    outputFormat: getStoredImageOutputFormat(),
    outputCompression: getStoredImageOutputCompression(),
  };
}

function serializeImageSizeSelection(selection: ImageSizeSelection): StoredImageSizeSelection {
  return {
    mode: selection.mode,
    aspectRatio: selection.aspectRatio,
    resolution: selection.resolution,
    customRatio: selection.customRatio,
    customWidth: selection.customWidth,
    customHeight: selection.customHeight,
  };
}

function restoreImageSizeSelection(stored: StoredImageSizeSelection | undefined, fallbackSize: string): ImageSizeSelection {
  const fallbackSelection = getImageSizeSelectionFromSize(fallbackSize);
  if (!stored) {
    return fallbackSelection;
  }
  return {
    mode: isImageSizeMode(stored.mode) ? stored.mode : fallbackSelection.mode,
    aspectRatio: isImageAspectRatio(stored.aspectRatio) ? stored.aspectRatio : fallbackSelection.aspectRatio,
    resolution: isImageResolution(stored.resolution) ? stored.resolution : fallbackSelection.resolution,
    customRatio: stored.customRatio || fallbackSelection.customRatio,
    customWidth: stored.customWidth || fallbackSelection.customWidth,
    customHeight: stored.customHeight || fallbackSelection.customHeight,
  };
}

function buildTurnOutcomeMessage(successCount: number, failedCount: number, cancelledCount: number) {
  const parts = [`成功 ${successCount} 张`];
  if (failedCount > 0) {
    parts.push(`失败 ${failedCount} 张`);
  }
  if (cancelledCount > 0) {
    parts.push(`终止 ${cancelledCount} 张`);
  }
  return parts.join("，");
}

function formatCreationTaskErrorMessage(message: string) {
  const trimmed = String(message || "").trim();
  if (!trimmed) {
    return "生成图片失败";
  }

  const normalized = trimmed.toLowerCase();
  if (normalized.includes("an error occurred while processing your request")) {
    const requestId = trimmed.match(/request id\s+([a-z0-9-]+)/i)?.[1];
    return [
      "提示词内容过多，或当前分辨率/质量组合过高。",
      "建议减少提示词内容，或降低分辨率、质量后重试。",
      requestId ? `请求 ID：${requestId}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  }
  if (normalized.includes("no images generated") && normalized.includes("model may have refused")) {
    return "没有生成图片，模型可能检测到敏感内容并拒绝了这次请求，请调整提示词后重试。";
  }
  if (normalized.includes("timed out waiting for async image generation")) {
    return "图片生成等待超时，建议稍后重试，或降低分辨率、质量后再试。";
  }
  if (normalized.includes("图片生成超过等待上限")) {
    return "上游生成耗时超过等待上限，请稍后重试或切换账号。";
  }
  if (
    normalized.includes("upstream returned cloudflare origin error page") ||
    normalized.includes("upstream returned cloudflare challenge page") ||
    (normalized.includes("cloudflare") && trimmed.includes("源服务器"))
  ) {
    return "上游 Cloudflare 临时异常，系统会自动冷却当前账号并换号重试。若仍失败，请稍后再试或切换代理/账号。";
  }
  if (normalized.includes("no available image quota")) {
    return "当前没有可用的图片额度，请检查账号额度或稍后重试。";
  }

  return trimmed;
}

function formatCreationTaskError(error: unknown, fallback = "生成图片失败") {
  return formatCreationTaskErrorMessage(error instanceof Error ? error.message : String(error || fallback));
}

function deriveTurnStatus(turn: ImageTurn): Pick<ImageTurn, "status" | "error"> {
  const loadingCount = turn.images.filter((image) => image.status === "loading").length;
  const failedCount = turn.images.filter((image) => image.status === "error").length;
  const successCount = turn.images.filter((image) => image.status === "success").length;
  const cancelledCount = turn.images.filter((image) => image.status === "cancelled").length;
  const messageCount = turn.images.filter((image) => image.status === "message").length;
  if (loadingCount > 0) {
    return { status: turn.status === "queued" ? "queued" : "generating", error: undefined };
  }
  if (failedCount > 0) {
    return { status: "error", error: buildTurnOutcomeMessage(successCount, failedCount, cancelledCount) };
  }
  if (cancelledCount > 0) {
    return { status: "cancelled", error: buildTurnOutcomeMessage(successCount, failedCount, cancelledCount) };
  }
  if (successCount > 0) {
    return { status: "success", error: undefined };
  }
  if (messageCount > 0) {
    return { status: "message", error: undefined };
  }
  return { status: "queued", error: undefined };
}

function isTurnInProgress(turn: ImageTurn) {
  return (
    turn.status === "queued" ||
    turn.status === "generating" ||
    turn.images.some((image) => image.status === "loading")
  );
}

function usesReferenceImages(mode: ImageConversationMode) {
  return mode === "image" || mode === "edit";
}

function usesResponseImageTaskModel(model: ImageModel) {
  return isResponseImageToolModel(model);
}

function isMissingBatchImageDataError(error?: string) {
  return typeof error === "string" && error.startsWith("未返回第 ") && error.endsWith(" 张图片数据");
}

function isMissingRecoverableTaskIdError(error?: string) {
  return error === MISSING_RECOVERABLE_TASK_ID_ERROR;
}

function isRecoverableCreationTaskError(error?: string) {
  return isMissingBatchImageDataError(error) || error === MISSING_CREATION_TASK_ERROR || isMissingRecoverableTaskIdError(error);
}

function getComposerConversationMode(composerMode: ComposerMode, referenceImages: StoredReferenceImage[]): ImageConversationMode {
  if (composerMode === "chat") {
    return "chat";
  }
  if (referenceImages.length === 0) {
    return "generate";
  }
  return referenceImages.some((image) => image.source === "conversation") ? "edit" : "image";
}

function imageQualityForModel(model: ImageModel, quality: ImageQuality) {
  return supportsImageQuality(model) ? quality : undefined;
}

function buildCreationTaskMessages(conversation: ImageConversation, activeTurnId: string): CreationTaskMessage[] {
  const messages: CreationTaskMessage[] = [];
  for (const turn of conversation.turns) {
    const prompt = turn.prompt.trim();
    if (prompt) {
      const content: CreationTaskMessage["content"] =
        turn.mode === "chat" && turn.referenceImages.length > 0
          ? [
              { type: "text", text: prompt },
              ...turn.referenceImages.map((image) => ({ type: "image_url" as const, image_url: { url: image.dataUrl } })),
            ]
          : prompt;
      messages.push({ role: "user", content });
    }
    if (turn.id === activeTurnId) {
      break;
    }

    const assistantParts = turn.images.flatMap((image) => {
      if (image.status === "message" && image.text_response?.trim()) {
        return [image.text_response.trim()];
      }
      if (image.status === "success" && image.revised_prompt?.trim()) {
        return [`Generated image: ${image.revised_prompt.trim()}`];
      }
      return [];
    });
    if (assistantParts.length > 0) {
      messages.push({ role: "assistant", content: assistantParts.join("\n\n") });
    }
  }
  return messages;
}

async function syncConversationCreationTasks(items: ImageConversation[], options: { persist?: boolean } = {}) {
  const taskIds = Array.from(
    new Set(
      items.flatMap((conversation) =>
        conversation.turns.flatMap((turn) =>
          turn.images.flatMap((image) => (image.status === "loading" && image.taskId ? [image.taskId] : [])),
        ),
      ),
    ),
  );
  if (taskIds.length === 0) {
    return items;
  }

  let taskList: Awaited<ReturnType<typeof fetchCreationTasks>>;
  try {
    taskList = await fetchCreationTasks(taskIds);
  } catch {
    return items;
  }
  const taskMap = new Map(taskList.items.map((task) => [task.id, task]));
  const missingTaskIds = new Set(taskList.missing_ids);
  let changed = false;
  const normalized = items.map((conversation) => {
    let completedActiveTurn = false;
    const turns = conversation.turns.map((turn) => {
      let turnChanged = false;
      const images = turn.images.map((image, imageIndex) => {
        if (image.status !== "loading" || !image.taskId) {
          return image;
        }
        if (missingTaskIds.has(image.taskId)) {
          return image;
        }
        const task = taskMap.get(image.taskId);
        if (!task) {
          return image;
        }
        const nextImage = taskDataToStoredImage(image, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility);
        if (nextImage !== image) {
          turnChanged = true;
        }
        return nextImage;
      });
      if (!turnChanged) {
        return turn;
      }
      changed = true;
      const derived = deriveTurnStatus({ ...turn, images });
      const nextTurn = {
        ...turn,
        ...derived,
        images,
      };
      if (isTurnInProgress(turn) && !isTurnInProgress(nextTurn)) {
        completedActiveTurn = true;
      }
      return nextTurn;
    });
    if (turns === conversation.turns || !turns.some((turn, index) => turn !== conversation.turns[index])) {
      return conversation;
    }
    const nextConversation = {
      ...conversation,
      turns,
    };
    return completedActiveTurn
      ? {
          ...nextConversation,
          updatedAt: new Date().toISOString(),
        }
      : nextConversation;
  });

  if (!changed) {
    return items;
  }
  if (options.persist !== false) {
    await saveImageConversations(normalized);
  }
  return normalized;
}

async function recoverConversationHistory(items: ImageConversation[]) {
  let changed = false;
  const normalized = items.map((conversation) => {
    const turns = conversation.turns.map((turn) => {
      let turnChanged = false;
      const recoveredImages = turn.images.map((image, imageIndex) => {
        if (image.status === "error" && isRecoverableCreationTaskError(image.error)) {
          turnChanged = true;
          return recoverStoredImageForResubmit(image, turn, imageIndex);
        }
        if (image.status === "loading" && !image.taskId) {
          turnChanged = true;
          return {
            ...image,
            taskId: recoverableImageTaskId(turn.id, imageIndex),
          };
        }
        return image;
      });

      if (turn.status !== "queued" && turn.status !== "generating") {
        if (!turnChanged) {
          return turn;
        }
        changed = true;
        const derived = deriveTurnStatus({ ...turn, status: "queued", images: recoveredImages });
        return {
          ...turn,
          ...derived,
          images: recoveredImages,
        };
      }

      const images = recoveredImages.map((image, imageIndex) => {
        if (image.status !== "loading" || image.taskId) {
          return image;
        }
        turnChanged = true;
        return {
          ...image,
          taskId: recoverableImageTaskId(turn.id, imageIndex),
          error: undefined,
        };
      });
      const derived = deriveTurnStatus({ ...turn, images });
      if (!turnChanged && derived.status === turn.status && derived.error === turn.error) {
        return turn;
      }
      changed = true;
      return {
        ...turn,
        ...derived,
        images,
      };
    });

    if (!turns.some((turn, index) => turn !== conversation.turns[index])) {
      return conversation;
    }

    return {
      ...conversation,
      turns,
      updatedAt: new Date().toISOString(),
    };
  });

  if (changed) {
    await saveImageConversations(normalized);
  }

  return syncConversationCreationTasks(normalized);
}


function ImagePageContent({ isAdmin }: { isAdmin: boolean }) {
  const location = useLocation();
  const isSubmitDispatchingRef = useRef(false);
  const foregroundPollingCountRef = useRef(0);
  const retryingImageIdsRef = useRef(new Set<string>());
  const cancelledTurnIdsRef = useRef(new Set<string>());
  const conversationsRef = useRef<ImageConversation[]>([]);
  const resultsViewportRef = useRef<HTMLDivElement>(null);
  const composerDockRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const editFileInputRef = useRef<HTMLInputElement>(null);
  const promptApplyRequestIdRef = useRef(0);
  const appliedURLPromptRef = useRef("");

  const [imagePrompt, setImagePrompt] = useState("");
  const [composerMode, setComposerMode] = useState<ComposerMode>(getStoredComposerMode);
  const [initialImagePreferences] = useState(getStoredImagePreferences);
  const [imageModel, setImageModel] = useState<ImageModel>(() => initialImagePreferences.model);
  const [imageCount, setImageCount] = useState("1");
  const initialImageSizeSelection = initialImagePreferences.sizeSelection;
  const [imageSizeMode, setImageSizeMode] = useState<ImageSizeMode>(() => initialImageSizeSelection.mode);
  const [imageAspectRatio, setImageAspectRatio] = useState<ImageAspectRatio>(() => initialImageSizeSelection.aspectRatio);
  const [imageResolution, setImageResolution] = useState<ImageResolution>(() => initialImageSizeSelection.resolution);
  const [imageCustomRatio, setImageCustomRatio] = useState(() => initialImageSizeSelection.customRatio);
  const [imageCustomWidth, setImageCustomWidth] = useState(() => initialImageSizeSelection.customWidth);
  const [imageCustomHeight, setImageCustomHeight] = useState(() => initialImageSizeSelection.customHeight);
  const [imageQuality, setImageQuality] = useState<ImageQuality>(() => initialImagePreferences.quality);
  const [imageOutputFormat, setImageOutputFormat] = useState<ImageOutputFormat>(() => initialImagePreferences.outputFormat);
  const [imageOutputCompression, setImageOutputCompression] = useState(() => initialImagePreferences.outputCompression);
  const [defaultImageVisibility, setDefaultImageVisibility] = useState<ImageVisibility>("private");
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [isPromptMarketOpen, setIsPromptMarketOpen] = useState(false);
  const [referenceImages, setReferenceImages] = useState<StoredReferenceImage[]>([]);
  const [conversations, setConversations] = useState<ImageConversation[]>([]);
  const [selectedConversationId, setSelectedConversationId] = useState<string | null>(null);
  const [isLoadingHistory, setIsLoadingHistory] = useState(true);
  const [lightboxImages, setLightboxImages] = useState<ImageLightboxItem[]>([]);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);
  const [deleteConfirm, setDeleteConfirm] = useState<{ type: "one"; id: string } | { type: "all" } | null>(null);
  const [editingTurnDraft, setEditingTurnDraft] = useState<EditingTurnDraft | null>(null);
  const [canRenderDesktopSidebar, setCanRenderDesktopSidebar] = useState(shouldRenderDesktopSidebar);
  const [progressByTurnKey, setProgressByTurnKey] = useState<Record<string, ImageTurnProgress>>(
    getImageTurnProgressSnapshot,
  );
  const [progressNow, setProgressNow] = useState(Date.now());
  const [composerDockHeight, setComposerDockHeight] = useState(0);
  const [visibilityMutatingImageKey, setVisibilityMutatingImageKey] = useState("");
  const [publicPromptPresets, setPublicPromptPresets] = useState<ImagePromptPreset[]>([]);
  const [publicPromptPresetStatus, setPublicPromptPresetStatus] = useState("");
  const [isLoadingPublicPromptPresets, setIsLoadingPublicPromptPresets] = useState(false);

  const parsedCount = useMemo(() => normalizeRequestedImageCount(imageCount), [imageCount]);
  const imageSize = useMemo(
    () =>
      buildImageSize({
        mode: imageSizeMode,
        aspectRatio: imageAspectRatio,
        resolution: imageResolution,
        customRatio: imageCustomRatio,
        customWidth: imageCustomWidth,
        customHeight: imageCustomHeight,
      }),
    [imageAspectRatio, imageCustomHeight, imageCustomRatio, imageCustomWidth, imageResolution, imageSizeMode],
  );
  const editingDraftImageSize = useMemo(() => {
    if (!editingTurnDraft || editingTurnDraft.mode === "chat") {
      return "";
    }
    return buildImageSize({
      mode: editingTurnDraft.sizeMode,
      aspectRatio: editingTurnDraft.aspectRatio,
      resolution: editingTurnDraft.resolution,
      customRatio: editingTurnDraft.customRatio,
      customWidth: editingTurnDraft.customWidth,
      customHeight: editingTurnDraft.customHeight,
    });
  }, [editingTurnDraft]);
  const editingDraftCustomRatioInvalid = editingTurnDraft
    ? isInvalidCustomRatioSelection(editingTurnDraft.sizeMode, editingTurnDraft.aspectRatio, editingTurnDraft.customRatio)
    : false;
  const editingDraftSizePreviewLabel =
    editingTurnDraft && editingTurnDraft.mode !== "chat"
      ? editingDraftImageSize
        ? formatImageSizeDisplay(editingDraftImageSize)
        : editingTurnDraft.sizeMode === "auto" ||
            (editingTurnDraft.sizeMode === "ratio" &&
              editingTurnDraft.resolution === "auto" &&
              !editingDraftCustomRatioInvalid)
          ? "Auto"
          : "尺寸无效"
      : "";
  const editingDraftSizePreviewDetail =
    editingTurnDraft?.sizeMode === "ratio"
      ? editingDraftCustomRatioInvalid
        ? "比例需要填写为宽:高"
        : editingTurnDraft.resolution === "auto"
          ? editingDraftImageSize
            ? `将按 ${editingDraftImageSize} 比例下发`
            : "Auto 比例将交给模型决定"
          : editingDraftImageSize
            ? `将下发计算后的 ${formatImageSizeDisplay(editingDraftImageSize)}`
            : "比例需要填写为宽:高"
      : editingTurnDraft?.sizeMode === "custom"
        ? editingDraftImageSize
          ? `已按链路限制校准为 ${formatImageSizeDisplay(editingDraftImageSize)}`
          : "宽高需要填写正整数"
        : "不会强制指定尺寸";
  const composerModelOptions = composerMode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_CREATION_MODEL_OPTIONS;
  const imageQualityOptions = supportsImageQuality(imageModel) ? IMAGE_QUALITY_OPTIONS : [];
  const selectedConversation = useMemo(
    () => conversations.find((item) => item.id === selectedConversationId) ?? null,
    [conversations, selectedConversationId],
  );
  const activeTaskCount = useMemo(
    () =>
      conversations.reduce((sum, conversation) => {
        const stats = getImageConversationStats(conversation);
        return sum + stats.queued + stats.running;
      }, 0),
    [conversations],
  );
  const deleteConfirmTitle = deleteConfirm?.type === "all" ? "清空历史记录" : deleteConfirm?.type === "one" ? "删除对话" : "";
  const deleteConfirmDescription =
    deleteConfirm?.type === "all"
      ? "确认删除全部图片历史记录吗？删除后无法恢复。"
      : deleteConfirm?.type === "one"
        ? "确认删除这条图片对话吗？删除后无法恢复。"
        : "";
  const imageOutputHint = useMemo(
    () => (
      <>
        <div>
          <span className="font-semibold text-stone-800">质量说明：</span>
          Low / Medium / High 会随请求下发，并补充到生成提示中；实际生效仍取决于当前账号和上游链路能力。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">输出格式：</span>
          支持 PNG / JPEG / WebP；PNG 不支持压缩率，JPEG / WebP 会下发 0-100 的压缩率参数。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">分辨率限制：</span>
          Free 账号建议按约 1.57M 像素总量控制；Paid 账号的图片最长边最高支持 3840。按比例选择 1080P / 2K / 4K，或手动指定宽高时，会把具体像素尺寸下发并补充到生成提示中。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">账号要求：</span>
          超过 Free 像素预算的分辨率档会优先使用 Plus / Pro / Team 等 Paid 账号；没有可用 Paid 账号时任务会直接提示失败原因。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">Auto 模式补充：</span>
          Auto 不会强制指定比例和分辨率，请直接在提示词里写明横竖版、画幅比例和目标输出尺寸。
        </div>
        <div className="mt-2">
          <span className="font-semibold text-stone-800">手动宽高：</span>
          手动输入的宽高会按生成链路要求校准为 16 的倍数，并控制在最长边 3840、宽高比不超过 3:1、总像素 655360-8294400 的范围内。
        </div>
      </>
    ),
    [],
  );

  useEffect(() => {
    if (!shouldLoadPublicPresetsOnStartup() || isLoadingHistory || conversations.length === 0) {
      if (!shouldLoadPublicPresetsOnStartup()) {
        setPublicPromptPresetStatus("移动端优先保障输入流畅。需要灵感时，打开市场或图库复用公开作品。");
      }
      return;
    }
    const controller = new AbortController();
    setIsLoadingPublicPromptPresets(true);
    setPublicPromptPresetStatus("");
    const cancelIdleWork = scheduleIdleStartupWork(() => {
      void fetchManagedImages({ scope: "public" }, { signal: controller.signal })
        .then((data) => {
          const presets = buildPublicGalleryPresets(data.items);
          setPublicPromptPresets(presets);
          setPublicPromptPresetStatus(presets.length > 0 ? "" : "暂无可复用的公开图库预设。可以直接输入提示词开始。");
        })
        .catch(() => {
          if (!controller.signal.aborted) {
            setPublicPromptPresets([]);
            setPublicPromptPresetStatus("公开图库预设暂时读取失败。可以直接输入提示词，不影响生成。");
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) {
            setIsLoadingPublicPromptPresets(false);
          }
        });
    });
    return () => {
      cancelIdleWork();
      controller.abort();
      setIsLoadingPublicPromptPresets(false);
    };
  }, [conversations.length, isLoadingHistory]);

  useEffect(() => {
    conversationsRef.current = conversations;
  }, [conversations]);

  useEffect(() => {
    const mediaQuery = window.matchMedia?.("(min-width: 1024px)");
    if (!mediaQuery) {
      return;
    }
    const updateDesktopSidebarPreference = () => {
      setCanRenderDesktopSidebar(mediaQuery.matches);
    };
    updateDesktopSidebarPreference();
    mediaQuery.addEventListener?.("change", updateDesktopSidebarPreference);
    return () => {
      mediaQuery.removeEventListener?.("change", updateDesktopSidebarPreference);
    };
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const promptFromURL = (params.get("prompt") || params.get("share_prompt") || "").trim();
    const requestKey = `${location.pathname}${location.search}`;
    if (!promptFromURL || appliedURLPromptRef.current === requestKey) {
      return;
    }
    appliedURLPromptRef.current = requestKey;
    promptApplyRequestIdRef.current += 1;
    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(promptFromURL);
    setReferenceImages([]);
    setDefaultImageVisibility("private");
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    window.setTimeout(() => textareaRef.current?.focus(), 50);
    toast.success("已填入分享提示词，可直接生成同款");
  }, [location.pathname, location.search]);

  useEffect(() => {
    const node = composerDockRef.current;
    if (!node) {
      return;
    }

    let frameId = 0;
    const measureComposerHeight = () => {
      frameId = 0;
      const nextHeight = Math.ceil(node.getBoundingClientRect().height);
      setComposerDockHeight((currentHeight) => (currentHeight === nextHeight ? currentHeight : nextHeight));
    };
    const scheduleComposerHeightMeasure = () => {
      if (frameId !== 0) {
        return;
      }
      frameId = window.requestAnimationFrame(measureComposerHeight);
    };

    scheduleComposerHeightMeasure();
    const observer = new ResizeObserver(scheduleComposerHeightMeasure);
    observer.observe(node);
    return () => {
      if (frameId !== 0) {
        window.cancelAnimationFrame(frameId);
      }
      observer.disconnect();
    };
  }, []);

  useEffect(() => {
    let cancelled = false;

    const refreshConversations = async () => {
      try {
        const items = await syncConversationCreationTasks(await listImageConversations());
        if (cancelled) {
          return;
        }
        conversationsRef.current = items;
        setConversations(items);
      } catch {
        // Background updates should not surface noisy toasts while the user is on another workflow.
      }
    };

    const handleConversationsChanged = () => {
      void refreshConversations();
    };

    window.addEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationsChanged);
    return () => {
      cancelled = true;
      window.removeEventListener(IMAGE_CONVERSATIONS_CHANGED_EVENT, handleConversationsChanged);
    };
  }, []);

  useEffect(
    () =>
      subscribeImageTurnProgress(() => {
        setProgressByTurnKey(getImageTurnProgressSnapshot());
      }),
    [],
  );

	  useEffect(() => {
	    if (activeTaskCount === 0 && Object.keys(progressByTurnKey).length === 0) {
	      return;
	    }

	    setProgressNow(Date.now());
	    const tickIntervalMs = imageProgressTickIntervalMs();
	    const timer = window.setInterval(() => {
	      setProgressNow(Date.now());
	    }, tickIntervalMs);
	    return () => {
	      window.clearInterval(timer);
	    };
	  }, [activeTaskCount, progressByTurnKey]);

  useEffect(() => {
    if (activeTaskCount === 0) {
      return;
    }

    let cancelled = false;
    let syncing = false;
    const syncActiveCreationTasks = async () => {
      if (syncing || foregroundPollingCountRef.current > 0 || document.hidden) {
        return;
      }
      syncing = true;
      const snapshot = conversationsRef.current;
      try {
        const nextConversations = await syncConversationCreationTasks(snapshot, { persist: false });
        if (!cancelled && conversationsRef.current === snapshot && nextConversations !== snapshot) {
          conversationsRef.current = nextConversations;
          setConversations(nextConversations);
          await saveImageConversations(nextConversations);
        }
      } catch {
        return;
      } finally {
        syncing = false;
      }
    };

	    void syncActiveCreationTasks();
	    const timer = window.setInterval(syncActiveCreationTasks, imageTaskPollingIntervalMs());
    const handleVisibilityChange = () => {
      if (!document.hidden) {
        void syncActiveCreationTasks();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      cancelled = true;
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.clearInterval(timer);
    };
  }, [activeTaskCount]);

  useEffect(() => {
    let cancelled = false;

    const loadHistory = async () => {
      try {
        const storedPreferences = getStoredImagePreferences();
        setImageModel(storedPreferences.model);
        setImageSizeMode(storedPreferences.sizeSelection.mode);
        setImageAspectRatio(storedPreferences.sizeSelection.aspectRatio);
        setImageResolution(storedPreferences.sizeSelection.resolution);
        setImageCustomRatio(storedPreferences.sizeSelection.customRatio);
        setImageCustomWidth(storedPreferences.sizeSelection.customWidth);
        setImageCustomHeight(storedPreferences.sizeSelection.customHeight);
        setImageQuality(storedPreferences.quality);
        setImageOutputFormat(storedPreferences.outputFormat);
        setImageOutputCompression(storedPreferences.outputCompression);

        const items = await listImageConversations();
        const normalizedItems = await recoverConversationHistory(items);
        if (cancelled) {
          return;
        }

        conversationsRef.current = normalizedItems;
        setConversations(normalizedItems);
        const storedConversationId =
          typeof window !== "undefined" ? window.localStorage.getItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY) : null;
        const nextSelectedConversationId =
          (storedConversationId && normalizedItems.some((conversation) => conversation.id === storedConversationId)
            ? storedConversationId
            : null) ?? pickFallbackConversationId(normalizedItems);
        setSelectedConversationId(nextSelectedConversationId);
      } catch (error) {
        const message = error instanceof Error ? error.message : "读取会话记录失败";
        toast.error(message);
      } finally {
        if (!cancelled) {
          setIsLoadingHistory(false);
        }
      }
    };

    const cancelHistoryLoad = scheduleInitialHistoryLoad(loadHistory);
    return () => {
      cancelled = true;
      cancelHistoryLoad();
    };
  }, []);

  useEffect(() => {
    if (!selectedConversationId) {
      return;
    }

    resultsViewportRef.current?.scrollTo({
      top: resultsViewportRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [selectedConversationId, selectedConversation?.turns.length]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    if (selectedConversationId) {
      window.localStorage.setItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY, selectedConversationId);
    } else {
      window.localStorage.removeItem(ACTIVE_IMAGE_CONVERSATION_STORAGE_KEY);
    }
  }, [selectedConversationId]);

  useEffect(() => {
    const handleOpenConversation = (event: Event) => {
      const conversationId = (event as CustomEvent<{ conversationId?: string }>).detail?.conversationId;
      if (conversationId) {
        setSelectedConversationId(conversationId);
      }
    };

    window.addEventListener(IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT, handleOpenConversation);
    return () => {
      window.removeEventListener(IMAGE_ACTIVE_CONVERSATION_REQUEST_EVENT, handleOpenConversation);
    };
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    const cancelPreferencePersist = scheduleImagePreferencePersist(() => {
      window.localStorage.setItem(COMPOSER_MODE_STORAGE_KEY, composerMode);
      window.localStorage.setItem(IMAGE_MODEL_STORAGE_KEY, imageModel);
      window.localStorage.setItem(IMAGE_SIZE_MODE_STORAGE_KEY, imageSizeMode);
      if (imageAspectRatio) {
        window.localStorage.setItem(IMAGE_ASPECT_RATIO_STORAGE_KEY, imageAspectRatio);
      } else {
        window.localStorage.removeItem(IMAGE_ASPECT_RATIO_STORAGE_KEY);
      }
      window.localStorage.setItem(IMAGE_RESOLUTION_STORAGE_KEY, imageResolution);
      window.localStorage.setItem(IMAGE_CUSTOM_RATIO_STORAGE_KEY, imageCustomRatio);
      window.localStorage.setItem(IMAGE_CUSTOM_WIDTH_STORAGE_KEY, imageCustomWidth);
      window.localStorage.setItem(IMAGE_CUSTOM_HEIGHT_STORAGE_KEY, imageCustomHeight);
      if (imageSize) {
        window.localStorage.setItem(IMAGE_SIZE_STORAGE_KEY, imageSize);
      } else {
        window.localStorage.removeItem(IMAGE_SIZE_STORAGE_KEY);
      }
      window.localStorage.setItem(IMAGE_SPEED_DEFAULTS_VERSION_STORAGE_KEY, IMAGE_SPEED_DEFAULTS_VERSION);
      window.localStorage.setItem(IMAGE_QUALITY_STORAGE_KEY, imageQuality);
      window.localStorage.setItem(IMAGE_OUTPUT_FORMAT_STORAGE_KEY, imageOutputFormat);
      const normalizedCompression = normalizeOutputCompressionValue(imageOutputCompression);
      if (normalizedCompression === undefined || imageOutputFormat === "png") {
        window.localStorage.removeItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY);
      } else {
        window.localStorage.setItem(IMAGE_OUTPUT_COMPRESSION_STORAGE_KEY, String(normalizedCompression));
      }
    });
    return cancelPreferencePersist;
  }, [
    composerMode,
    imageAspectRatio,
    imageCustomHeight,
    imageCustomRatio,
    imageCustomWidth,
    imageModel,
    imageOutputCompression,
    imageOutputFormat,
    imageQuality,
    imageResolution,
    imageSize,
    imageSizeMode,
  ]);

  useEffect(() => {
    if (composerMode === "chat") {
      if (!isChatModel(imageModel)) {
        setImageModel(DEFAULT_CHAT_MODEL);
      }
      return;
    }

    if (!isImageCreationModel(imageModel)) {
      setImageModel(DEFAULT_IMAGE_MODEL);
    }
  }, [composerMode, imageModel]);

  useEffect(() => {
    if (selectedConversationId && !conversations.some((conversation) => conversation.id === selectedConversationId)) {
      setSelectedConversationId(pickFallbackConversationId(conversations));
    }
  }, [conversations, selectedConversationId]);

  const persistConversation = async (conversation: ImageConversation) => {
    const nextConversations = sortImageConversations([
      conversation,
      ...conversationsRef.current.filter((item) => item.id !== conversation.id),
    ]);
    conversationsRef.current = nextConversations;
    setConversations(nextConversations);
    await saveImageConversation(conversation);
  };

  const updateConversation = useCallback(
    async (
      conversationId: string,
      updater: (current: ImageConversation | null) => ImageConversation,
      options: { persist?: boolean } = {},
    ) => {
      const current = conversationsRef.current.find((item) => item.id === conversationId) ?? null;
      const nextConversation = updater(current);
      const nextConversations = sortImageConversations([
        nextConversation,
        ...conversationsRef.current.filter((item) => item.id !== conversationId),
      ]);
      conversationsRef.current = nextConversations;
      setConversations(nextConversations);
      if (options.persist !== false) {
        await saveImageConversation(nextConversation);
      }
    },
    [],
  );

  const updateTurnProgress = useCallback(
    (conversationId: string, turnId: string, updates: Omit<ImageTurnProgress, "startedAt"> & { startedAt?: number }) => {
      setImageTurnProgress(conversationId, turnId, updates);
    },
    [],
  );

  const clearTurnProgress = useCallback((conversationId: string, turnId: string) => {
    clearImageTurnProgress(conversationId, turnId);
  }, []);

  const clearComposerInputs = useCallback(() => {
    promptApplyRequestIdRef.current += 1;
    setImagePrompt("");
    setImageCount("1");
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
    setReferenceImages([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, []);

  const resetComposer = useCallback(() => {
    clearComposerInputs();
  }, [clearComposerInputs]);

  const handleComposerModeChange = useCallback((mode: ComposerMode) => {
    setComposerMode(mode);
    if (mode === "chat") {
      promptApplyRequestIdRef.current += 1;
      setDefaultImageVisibility("private");
      setReferenceImages([]);
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  }, []);

  const handleCreateDraft = () => {
    setSelectedConversationId(null);
    resetComposer();
    textareaRef.current?.focus();
  };

  const handleApplyPromptPreset = useCallback(async (preset: ImagePromptPreset) => {
    const requestId = promptApplyRequestIdRef.current + 1;
    promptApplyRequestIdRef.current = requestId;
    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(preset.prompt);
    setImageCount(String(preset.count));
    const presetSizeSelection = getImageSizeSelectionFromSize(preset.size);
    setImageSizeMode(presetSizeSelection.mode);
    setImageAspectRatio(presetSizeSelection.aspectRatio);
    setImageResolution(presetSizeSelection.resolution);
    setImageCustomRatio(presetSizeSelection.customRatio);
    setImageCustomWidth(presetSizeSelection.customWidth);
    setImageCustomHeight(presetSizeSelection.customHeight);
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
    setReferenceImages([]);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();

    const toastId = toast.loading("正在读取参考图");
    try {
      const referenceImage = await buildReferenceImageFromUrl(preset.imageSrc, 0, "preset-reference");
      if (promptApplyRequestIdRef.current !== requestId) {
        toast.dismiss(toastId);
        return;
      }
      setReferenceImages([referenceImage]);
      toast.dismiss(toastId);
      toast.success("已套用提示词和参考图");
    } catch {
      if (promptApplyRequestIdRef.current !== requestId) {
        toast.dismiss(toastId);
        return;
      }
      toast.dismiss(toastId);
      toast.error("已套用提示词，但参考图读取失败");
    }
  }, []);

  const handleApplyMarketPrompt = useCallback(async (prompt: BananaPrompt) => {
    const referenceImageUrls = getPromptReferenceImageUrls(prompt);
    const requestId = promptApplyRequestIdRef.current + 1;
    promptApplyRequestIdRef.current = requestId;

    setSelectedConversationId(null);
    setComposerMode("image");
    setImagePrompt(prompt.prompt);
    setImageCount("1");
    setImageSizeMode("auto");
    setImageAspectRatio("");
    setImageResolution("auto");
    setImageCustomRatio(DEFAULT_IMAGE_CUSTOM_RATIO);
    setImageCustomWidth(DEFAULT_IMAGE_CUSTOM_WIDTH);
    setImageCustomHeight(DEFAULT_IMAGE_CUSTOM_HEIGHT);
    setImageOutputFormat(DEFAULT_IMAGE_OUTPUT_FORMAT);
    setImageOutputCompression("");
    setDefaultImageVisibility("private");
    setReferenceImages([]);
    setIsPromptMarketOpen(false);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    textareaRef.current?.focus();

    if (referenceImageUrls.length === 0) {
      toast.success("已套用提示词");
      return;
    }

    const toastId = toast.loading(`正在读取 ${referenceImageUrls.length} 张参考图`);
    const results = await Promise.allSettled(
      referenceImageUrls.map((url, index) => buildReferenceImageFromUrl(url, index, "prompt-reference")),
    );
    const loadedReferences = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));

    toast.dismiss(toastId);
    if (promptApplyRequestIdRef.current !== requestId) {
      return;
    }
    if (loadedReferences.length > 0) {
      setReferenceImages(loadedReferences);
    }
    if (loadedReferences.length === referenceImageUrls.length) {
      toast.success("已套用提示词和参考图");
    } else if (loadedReferences.length > 0) {
      toast.error(`已套用提示词，${referenceImageUrls.length - loadedReferences.length} 张参考图读取失败`);
    } else {
      toast.error("已套用提示词，但参考图读取失败");
    }
  }, []);

  const handleDeleteConversation = async (id: string) => {
    const nextConversations = conversations.filter((item) => item.id !== id);
    conversationsRef.current = nextConversations;
    setConversations(nextConversations);
    if (selectedConversationId === id) {
      setSelectedConversationId(pickFallbackConversationId(nextConversations));
      resetComposer();
    }

    try {
      await deleteImageConversation(id);
    } catch (error) {
      const message = error instanceof Error ? error.message : "删除会话失败";
      toast.error(message);
      const items = await listImageConversations();
      conversationsRef.current = items;
      setConversations(items);
    }
  };

  const handleClearHistory = async () => {
    try {
      await clearImageConversations();
      conversationsRef.current = [];
      setConversations([]);
      setSelectedConversationId(null);
      resetComposer();
      toast.success("已清空历史记录");
    } catch (error) {
      const message = error instanceof Error ? error.message : "清空历史记录失败";
      toast.error(message);
    }
  };

  const openDeleteConversationConfirm = (id: string) => {
    setIsHistoryOpen(false);
    setDeleteConfirm({ type: "one", id });
  };

  const openClearHistoryConfirm = () => {
    setIsHistoryOpen(false);
    setDeleteConfirm({ type: "all" });
  };

  const handleConfirmDelete = async () => {
    const target = deleteConfirm;
    setDeleteConfirm(null);
    if (!target) {
      return;
    }
    if (target.type === "all") {
      await handleClearHistory();
      return;
    }
    await handleDeleteConversation(target.id);
  };

  const appendReferenceImages = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    promptApplyRequestIdRef.current += 1;

    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );

      setReferenceImages((prev) => [...prev, ...previews]);
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取参考图失败";
      toast.error(message);
    }
  }, []);

  const handleReferenceImageChange = useCallback(
    async (files: File[]) => {
      if (files.length === 0) {
        return;
      }

      await appendReferenceImages(files);
    },
    [appendReferenceImages],
  );

  const handleRemoveReferenceImage = useCallback((index: number) => {
    setReferenceImages((prev) => {
      const next = prev.filter((_, currentIndex) => currentIndex !== index);
      if (next.length === 0 && fileInputRef.current) {
        fileInputRef.current.value = "";
      }
      return next;
    });
  }, []);

  const handleContinueEdit = useCallback(
    async (conversationId: string, image: StoredImage | StoredReferenceImage) => {
      try {
        const nextReference =
          "dataUrl" in image
            ? {
                referenceImage: image,
              }
            : await buildReferenceImageFromStoredImage(image, `conversation-${conversationId}-${Date.now()}.png`);
        if (!nextReference) {
          return;
        }

        setSelectedConversationId(conversationId);
        setComposerMode("image");
        setReferenceImages((prev) => [
          ...prev,
          {
            ...nextReference.referenceImage,
            source: "conversation",
          },
        ]);
        setImagePrompt("");
        textareaRef.current?.focus();
        toast.success("已加入当前参考图，继续输入描述即可编辑");
      } catch (error) {
        const message = error instanceof Error ? error.message : "读取结果图失败";
        toast.error(message);
      }
    },
    [],
  );

  const openLightbox = useCallback((images: ImageLightboxItem[], index: number) => {
    if (images.length === 0) {
      return;
    }

    setLightboxImages(images);
    setLightboxIndex(Math.max(0, Math.min(index, images.length - 1)));
    setLightboxOpen(true);
  }, []);

  const handleImageVisibilityChange = useCallback(
    async (conversationId: string, turnId: string, imageIndex: number, visibility: ImageVisibility) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      const targetImage = targetTurn?.images[imageIndex];
      if (!targetConversation || !targetTurn || !targetImage) {
        toast.error("未找到对应的图片记录");
        return;
      }
      if (targetImage.status !== "success") {
        toast.error("图片生成成功后才能修改公开状态");
        return;
      }
      const path = targetImage.path || (targetImage.url ? getManagedImagePathFromUrl(targetImage.url) : "");
      if (!path) {
        toast.error("未找到可同步到图库的图片路径");
        return;
      }

      const mutatingKey = `${conversationId}:${turnId}:${targetImage.id}`;
      if (visibilityMutatingImageKey === mutatingKey) {
        return;
      }
      if (visibilityMutatingImageKey) {
        return;
      }
      setVisibilityMutatingImageKey(mutatingKey);
      try {
        const data = await updateManagedImageVisibility(path, visibility);
        const updatedVisibility = data.item.visibility || visibility;
        const updatedPath = data.item.path || path;
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? targetConversation;
          return {
            ...conversation,
            updatedAt: new Date().toISOString(),
            turns: conversation.turns.map((turn) =>
              turn.id === turnId
                ? {
                    ...turn,
                    images: turn.images.map((image, index) =>
                      index === imageIndex
                        ? {
                            ...image,
                            path: updatedPath,
                            visibility: updatedVisibility,
                          }
                        : image,
                    ),
                  }
                : turn,
            ),
          };
        });
        clearImageManagerCache();
        toast.success(updatedVisibility === "public" ? "已公开到公开图库" : "已取消公开");
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "更新公开状态失败");
      } finally {
        setVisibilityMutatingImageKey("");
      }
    },
    [updateConversation, visibilityMutatingImageKey],
  );

  const openEditTurnDialog = useCallback((conversationId: string, turnId: string) => {
    const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
    const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
    if (!targetConversation || !targetTurn) {
      toast.error("未找到对应的对话轮次");
      return;
    }
    if (isTurnInProgress(targetTurn)) {
      toast.error("当前轮次正在处理，稍后再编辑");
      return;
    }
    const sizeSelection = restoreImageSizeSelection(targetTurn.sizeSelection, targetTurn.size);
    setEditingTurnDraft({
      conversationId,
      turnId,
      prompt: targetTurn.prompt,
      model:
        targetTurn.mode === "chat"
          ? isChatModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_CHAT_MODEL
          : isImageCreationModel(targetTurn.model)
            ? targetTurn.model
            : DEFAULT_IMAGE_MODEL,
      mode: targetTurn.mode,
      count: targetTurn.mode === "chat" ? "1" : String(normalizeRequestedImageCount(targetTurn.count || targetTurn.images.length || 1)),
      sizeMode: targetTurn.mode === "chat" ? "auto" : sizeSelection.mode,
      aspectRatio: targetTurn.mode === "chat" ? "" : sizeSelection.aspectRatio,
      resolution: targetTurn.mode === "chat" ? "auto" : sizeSelection.resolution,
      customRatio: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_RATIO : sizeSelection.customRatio,
      customWidth: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_WIDTH : sizeSelection.customWidth,
      customHeight: targetTurn.mode === "chat" ? DEFAULT_IMAGE_CUSTOM_HEIGHT : sizeSelection.customHeight,
      quality: targetTurn.quality || DEFAULT_IMAGE_QUALITY,
      outputFormat: targetTurn.outputFormat || DEFAULT_IMAGE_OUTPUT_FORMAT,
      outputCompression:
        targetTurn.outputCompression === undefined || targetTurn.outputCompression === null
          ? ""
          : String(targetTurn.outputCompression),
      visibility: targetTurn.visibility || "private",
      referenceImages: targetTurn.referenceImages,
    });
  }, []);

  const handleEditReferenceImageChange = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );
      setEditingTurnDraft((current) =>
        current
          ? {
              ...current,
              referenceImages: [...current.referenceImages, ...previews],
            }
          : current,
      );
      if (editFileInputRef.current) {
        editFileInputRef.current.value = "";
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取参考图失败";
      toast.error(message);
    }
  }, []);

  const handleRemoveEditReferenceImage = useCallback((index: number) => {
    setEditingTurnDraft((current) =>
      current
        ? {
            ...current,
            referenceImages: current.referenceImages.filter((_, currentIndex) => currentIndex !== index),
          }
        : current,
    );
  }, []);

  const runConversationQueue = useCallback(
    async (conversationId: string) => {
      if (activeConversationQueueIds.has(conversationId)) {
        return;
      }

      const snapshot = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const activeTurn = snapshot?.turns.find(
        (turn) =>
          (turn.status === "queued" || turn.status === "generating") &&
          turn.images.some((image) => image.status === "loading"),
      );
      if (!snapshot || !activeTurn) {
        return;
      }

      activeConversationQueueIds.add(conversationId);
      foregroundPollingCountRef.current += 1;
      const activeTurnKey = imageTurnProgressKey(conversationId, activeTurn.id);
      updateTurnProgress(conversationId, activeTurn.id, {
        message: activeTurn.mode === "chat" ? "正在准备对话请求" : "正在准备生成任务",
        detail:
          activeTurn.mode === "chat"
            ? "正在整理上下文"
            : `准备处理 ${activeTurn.images.filter((image) => image.status === "loading").length || activeTurn.count} 张图片`,
      });
      const applyTasks = async (tasks: CreationTask[]) => {
        const taskMap = new Map(tasks.map((task) => [task.id, task]));
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          let completedActiveTurn = false;
          const turns = conversation.turns.map((turn) => {
            if (turn.id !== activeTurn.id) {
              return turn;
            }
            const images = turn.images.map((image, imageIndex) => {
              const taskId = image.taskId || image.id;
              const task = taskMap.get(taskId);
              const taskImage = image.taskId === taskId ? image : { ...image, taskId };
              return task ? taskDataToStoredImage(taskImage, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility) : image;
            });
            const derived = deriveTurnStatus({ ...turn, status: "generating", images });
            const nextTurn = {
              ...turn,
              ...derived,
              images,
            };
            if (isTurnInProgress(turn) && !isTurnInProgress(nextTurn)) {
              completedActiveTurn = true;
            }
            return nextTurn;
          });
          const nextConversation = {
            ...conversation,
            turns,
          };
          return completedActiveTurn
            ? {
                ...nextConversation,
                updatedAt: new Date().toISOString(),
              }
            : nextConversation;
        });
      };

      try {
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          return {
            ...conversation,
            turns: conversation.turns.map((turn) =>
              turn.id === activeTurn.id
                ? {
                    ...turn,
                    status: "generating",
                    error: undefined,
                    images: turn.images.map((image, imageIndex) =>
                      image.status === "loading"
                        ? {
                            ...image,
                            taskId: imageTaskIdForImage(turn.id, turn.images, imageIndex),
                          }
                        : image,
                    ),
                  }
                : turn,
            ),
          };
        });

        updateTurnProgress(conversationId, activeTurn.id, {
          message:
            activeTurn.mode === "chat" ? "正在准备对话请求" : usesReferenceImages(activeTurn.mode) ? "正在整理参考图" : "正在准备生成请求",
          detail:
            activeTurn.mode === "chat"
              ? "正在整理上下文并创建后台任务"
              : usesReferenceImages(activeTurn.mode)
                ? "正在读取参考图并准备上传"
                : "正在创建图片生成任务",
        });
        const referenceFiles = activeTurn.referenceImages.map((image, index) =>
          dataUrlToFile(image.dataUrl, image.name || `${activeTurn.id}-${index + 1}.png`, image.type),
        );
        if (usesReferenceImages(activeTurn.mode) && referenceFiles.length === 0) {
          throw new Error("未找到可用的参考图");
        }
        const taskMessages = buildCreationTaskMessages(snapshot, activeTurn.id);
        const taskOutputFormat = activeTurn.outputFormat || DEFAULT_IMAGE_OUTPUT_FORMAT;
        const taskOutputCompression = imageOutputCompressionForFormat(taskOutputFormat, activeTurn.outputCompression);
        const pendingTaskGroups = activeTurn.images.reduce<Array<{ taskId: string; count: number }>>(
          (groups, image, imageIndex) => {
            if (image.status !== "loading") {
              return groups;
            }
            const taskId = imageTaskIdForImage(activeTurn.id, activeTurn.images, imageIndex);
            const existing = groups.find((group) => group.taskId === taskId);
            if (existing) {
              existing.count += 1;
            } else {
              groups.push({ taskId, count: 1 });
            }
            return groups;
          },
          [],
        );
        const submitTaskGroup = (group: { taskId: string; count: number }) => {
          if (activeTurn.mode === "chat") {
            return createChatCompletionTask(group.taskId, activeTurn.prompt, activeTurn.model, taskMessages);
          }
          if (usesResponseImageTaskModel(activeTurn.model)) {
            return createResponseImageGenerationTask(
              group.taskId,
              activeTurn.prompt,
              activeTurn.model,
              activeTurn.size,
              imageQualityForModel(activeTurn.model, activeTurn.quality || DEFAULT_IMAGE_QUALITY),
              group.count,
              taskMessages,
              activeTurn.referenceImages.map((image) => image.dataUrl),
              activeTurn.visibility || "private",
              activeTurn.sizeSelection?.resolution,
              taskOutputFormat,
              taskOutputCompression,
            );
          }
          if (usesReferenceImages(activeTurn.mode)) {
            return createImageEditTask(
              group.taskId,
              referenceFiles,
              activeTurn.prompt,
              activeTurn.model,
              activeTurn.size,
              imageQualityForModel(activeTurn.model, activeTurn.quality || DEFAULT_IMAGE_QUALITY),
              group.count,
              taskMessages,
              activeTurn.visibility || "private",
              activeTurn.sizeSelection?.resolution,
              taskOutputFormat,
              taskOutputCompression,
            );
          }
          return createImageGenerationTask(
            group.taskId,
            activeTurn.prompt,
            activeTurn.model,
            activeTurn.size,
            imageQualityForModel(activeTurn.model, activeTurn.quality || DEFAULT_IMAGE_QUALITY),
            group.count,
            taskMessages,
            activeTurn.visibility || "private",
            activeTurn.sizeSelection?.resolution,
            taskOutputFormat,
            taskOutputCompression,
          );
        };
        updateTurnProgress(conversationId, activeTurn.id, {
          message: activeTurn.mode === "chat" ? "正在提交对话请求" : "正在提交生成请求",
          detail: activeTurn.mode === "chat" ? "对话任务正在入队" : `${pendingTaskGroups.length} 个图片任务正在入队`,
        });
        const submitted = await Promise.all(pendingTaskGroups.map(submitTaskGroup));
        await applyTasks(submitted);
        updateTurnProgress(conversationId, activeTurn.id, {
          message: activeTurn.mode === "chat" ? "等待对话回复" : "等待生成结果",
          detail: activeTurn.mode === "chat" ? "请求已提交，正在等待首包" : `已提交 ${pendingTaskGroups.length} 个任务，正在等待上游开始生成`,
        });

        let consecutivePollFailures = 0;
        while (true) {
          const latestConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
          const latestTurn = latestConversation?.turns.find((turn) => turn.id === activeTurn.id);
          const loadingTaskIds = Array.from(
            new Set(
              latestTurn?.images.flatMap((image) =>
                image.status === "loading" && image.taskId ? [image.taskId] : [],
              ) || [],
            ),
          );
          if (loadingTaskIds.length === 0) {
            break;
          }

          await sleep(CREATION_TASK_POLL_INTERVAL_MS);
          let taskList: Awaited<ReturnType<typeof fetchCreationTasks>>;
          try {
            taskList = await fetchCreationTasks(loadingTaskIds);
            consecutivePollFailures = 0;
          } catch (error) {
            consecutivePollFailures += 1;
            if (consecutivePollFailures >= CREATION_TASK_POLL_RETRY_LIMIT) {
              throw new Error(formatCreationTaskError(error, activeTurn.mode === "chat" ? "同步对话状态失败" : "同步生成状态失败"));
            }
            updateTurnProgress(conversationId, activeTurn.id, {
              message: activeTurn.mode === "chat" ? "正在恢复对话状态" : "正在恢复生成状态",
              detail: `状态同步失败，正在第 ${consecutivePollFailures} 次重试`,
            });
            await sleep(Math.min(10000, 1000 * consecutivePollFailures));
            continue;
          }
          if (taskList.items.length > 0) {
            await applyTasks(taskList.items);
            const queuedCount = taskList.items.filter((item) => item.status === "queued").length;
            const runningCount = taskList.items.filter((item) => item.status === "running").length;
            const successCount = taskList.items.filter((item) => item.status === "success").length;
            updateTurnProgress(conversationId, activeTurn.id, {
              message: activeTurn.mode === "chat" ? "等待对话回复" : "等待生成结果",
              detail:
                activeTurn.mode === "chat"
                  ? runningCount > 0
                    ? "上游正在生成回复"
                    : queuedCount > 0
                      ? "请求已入队，等待上游处理"
                      : "正在同步回复结果"
                  : runningCount > 0
                    ? `上游生成中：${runningCount} 张，已完成 ${successCount} 张`
                    : queuedCount > 0
                      ? `排队中：${queuedCount} 张，等待可用账号/并发槽位`
                      : `正在同步结果，剩余 ${loadingTaskIds.length} 张`,
            });
          }
          if (taskList.missing_ids.length > 0 && latestTurn) {
            updateTurnProgress(conversationId, activeTurn.id, {
              message: activeTurn.mode === "chat" ? "正在恢复对话任务" : "正在恢复生成任务",
              detail: `${taskList.missing_ids.length} 个任务状态丢失，正在重新提交`,
            });
            const missingTaskGroups = taskList.missing_ids.flatMap((taskId) => {
              const count = latestTurn.images.filter((image) => image.status === "loading" && image.taskId === taskId).length;
              return count > 0 ? [{ taskId, count }] : [];
            });
            const settled = await Promise.allSettled(missingTaskGroups.map(submitTaskGroup));
            const resubmitted = settled.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
            if (resubmitted.length > 0) {
              await applyTasks(resubmitted);
            }
            if (resubmitted.length === 0 && settled.some((result) => result.status === "rejected")) {
              consecutivePollFailures += 1;
              if (consecutivePollFailures >= CREATION_TASK_POLL_RETRY_LIMIT) {
                const failed = settled.find((result) => result.status === "rejected");
                throw new Error(
                  formatCreationTaskError(
                    failed?.status === "rejected" ? failed.reason : undefined,
                    activeTurn.mode === "chat" ? "恢复对话任务失败" : "恢复生成任务失败",
                  ),
                );
              }
              await sleep(Math.min(10000, 1000 * consecutivePollFailures));
            }
          }
        }

        updateTurnProgress(conversationId, activeTurn.id, {
          message: activeTurn.mode === "chat" ? "回复完成" : "生成完成",
          detail: "正在刷新会话",
        });
        if (activeTurn.mode !== "chat") {
          window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
        }
      } catch (error) {
        const message = formatCreationTaskError(error, activeTurn.mode === "chat" ? "对话请求失败" : "生成图片失败");
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? snapshot;
          return {
            ...conversation,
            updatedAt: new Date().toISOString(),
            turns: conversation.turns.map((turn) =>
              turn.id === activeTurn.id
                ? {
                    ...turn,
                    status: "error",
                    error: message,
                    images: turn.images.map((image) =>
                      image.status === "loading" ? { ...image, status: "error", error: message } : image,
                    ),
                  }
                : turn,
            ),
          };
        });
        toast.error(message);
      } finally {
        clearTurnProgress(conversationId, activeTurn.id);
        cancelledTurnIdsRef.current.delete(activeTurnKey);
        activeConversationQueueIds.delete(conversationId);
        foregroundPollingCountRef.current = Math.max(0, foregroundPollingCountRef.current - 1);
        for (const conversation of conversationsRef.current) {
          if (
            !activeConversationQueueIds.has(conversation.id) &&
            conversation.turns.some(
              (turn) =>
                (turn.status === "queued" || turn.status === "generating") &&
                turn.images.some((image) => image.status === "loading"),
            )
          ) {
            void runConversationQueue(conversation.id);
          }
        }
      }
    },
    [clearTurnProgress, updateConversation, updateTurnProgress],
  );
  useEffect(() => {
    for (const conversation of conversations) {
      if (
        !activeConversationQueueIds.has(conversation.id) &&
        conversation.turns.some(
          (turn) =>
            (turn.status === "queued" || turn.status === "generating") &&
            turn.images.some((image) => image.status === "loading"),
        )
      ) {
        void runConversationQueue(conversation.id);
      }
    }
  }, [conversations, runConversationQueue]);

  const handleCancelTurn = useCallback(
    async (conversationId: string, turnId: string) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      const taskIds = Array.from(
        new Set(targetTurn.images.flatMap((image) => (image.status === "loading" && image.taskId ? [image.taskId] : []))),
      );
      if (taskIds.length === 0) {
        if (targetTurn.mode === "chat") {
          const turnKey = imageTurnProgressKey(conversationId, turnId);
          cancelledTurnIdsRef.current.add(turnKey);
          clearTurnProgress(conversationId, turnId);
          await updateConversation(conversationId, (current) => {
            const conversation = current ?? targetConversation;
            return {
              ...conversation,
              updatedAt: new Date().toISOString(),
              turns: conversation.turns.map((turn) => {
                if (turn.id !== turnId) {
                  return turn;
                }
                const images = turn.images.map((image) =>
                  image.status === "loading"
                    ? {
                        ...image,
                        status: "cancelled" as const,
                        error: "请求已终止",
                      }
                    : image,
                );
                return {
                  ...turn,
                  ...deriveTurnStatus({ ...turn, images }),
                  images,
                };
              }),
            };
          });
          toast.success("已终止对话请求");
        }
        return;
      }

      const results = await Promise.allSettled(taskIds.map((taskId) => cancelCreationTask(taskId)));
      const taskMap = new Map(
        results.flatMap((result) => (result.status === "fulfilled" ? [[result.value.id, result.value] as const] : [])),
      );
      const failedRequests = results.filter((result) => result.status === "rejected").length;

      await updateConversation(conversationId, (current) => {
        const conversation = current ?? targetConversation;
        return {
          ...conversation,
          updatedAt: new Date().toISOString(),
          turns: conversation.turns.map((turn) => {
            if (turn.id !== turnId) {
              return turn;
            }
            const images = turn.images.map((image, imageIndex) => {
              if (image.status !== "loading") {
                return image;
              }
              const taskId = image.taskId || image.id;
              const task = taskMap.get(taskId);
              if (task) {
                return taskDataToStoredImage({ ...image, taskId }, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility);
              }
              return {
                ...image,
                taskId,
                status: "cancelled" as const,
                error: failedRequests > 0 ? "终止请求失败，已在本地停止等待" : "任务已终止",
              };
            });
            const derived = deriveTurnStatus({ ...turn, images });
            return {
              ...turn,
              ...derived,
              images,
            };
          }),
        };
      });

      if (failedRequests > 0) {
        toast.error(`部分终止请求失败：${failedRequests}/${taskIds.length}`);
      } else {
        toast.success("已终止生成任务");
      }
    },
    [clearTurnProgress, updateConversation],
  );

  const handleRetryImage = useCallback(
    async (conversationId: string, turnId: string, imageIndex: number) => {
      const retryKey = `${conversationId}:${turnId}:${imageIndex}`;
      if (retryingImageIdsRef.current.has(retryKey)) {
        return;
      }

      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      const targetImage = targetTurn?.images[imageIndex];
      if (!targetConversation || !targetTurn || !targetImage) {
        toast.error("未找到对应的图片记录");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再重试");
        return;
      }
      if (!targetTurn.prompt.trim()) {
        toast.error("请输入提示词");
        return;
      }
      if (targetImage.status !== "error" && targetImage.status !== "message") {
        toast.error("只有失败图片或模型文本回复可以单独重试");
        return;
      }
      if (usesReferenceImages(targetTurn.mode) && targetTurn.referenceImages.length === 0) {
        toast.error("未找到可用的参考图");
        return;
      }

      retryingImageIdsRef.current.add(retryKey);
      const now = new Date().toISOString();
      const retryTaskId = imageTaskBatchId(`${targetTurn.id}-${createId()}`, imageIndex);
      try {
        await updateConversation(conversationId, (current) => {
          const conversation = current ?? targetConversation;
          return {
            ...conversation,
            updatedAt: now,
            turns: conversation.turns.map((turn) => {
              if (turn.id !== turnId) {
                return turn;
              }
              const images = turn.images.map((image, index) =>
                index === imageIndex
                  ? {
                      ...image,
                      taskId: retryTaskId,
                      status: "loading" as const,
                      b64_json: undefined,
                      url: undefined,
                      path: undefined,
                      width: undefined,
                      height: undefined,
                      resolution: undefined,
                      visibility: targetTurn.mode === "chat" ? undefined : targetTurn.visibility || "private",
                      revised_prompt: undefined,
                      text_response: undefined,
                      error: undefined,
                    }
                  : image,
              );
              const derived = deriveTurnStatus({ ...turn, status: "queued", images });
              return {
                ...turn,
                ...derived,
                images,
              };
            }),
          };
        });
        void runConversationQueue(conversationId);
        toast.success("已加入重试队列");
      } catch (error) {
        toast.error(formatCreationTaskError(error, "提交重试失败"));
      } finally {
        retryingImageIdsRef.current.delete(retryKey);
      }
    },
    [runConversationQueue, updateConversation],
  );

  const handleRegenerateTurn = useCallback(
    async (conversationId: string, turnId: string) => {
      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      if (!targetTurn.prompt.trim()) {
        toast.error("请输入提示词");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再重新生成");
        return;
      }
      if (usesReferenceImages(targetTurn.mode) && targetTurn.referenceImages.length === 0) {
        toast.error("未找到可用的参考图");
        return;
      }

      const now = new Date().toISOString();
      const regenerationId = createId();
      await updateConversation(conversationId, (current) => {
        const conversation = current ?? targetConversation;
        const isFirstTurn = conversation.turns[0]?.id === turnId;
        return {
          ...conversation,
          title: isFirstTurn ? buildConversationTitle(targetTurn.prompt) : conversation.title,
          updatedAt: now,
          turns: conversation.turns.map((turn) => {
            if (turn.id !== turnId) {
              return turn;
            }

            const imageCount = turn.mode === "chat" ? 1 : normalizeRequestedImageCount(turn.count || turn.images.length || 1);
            const visibility = turn.mode === "chat" ? undefined : turn.visibility || "private";
            return {
              ...turn,
              count: imageCount,
              status: "queued",
              error: undefined,
              images: Array.from({ length: imageCount }, (_, index) => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  status: "loading" as const,
                  visibility,
                };
              }),
            };
          }),
        };
      });
      void runConversationQueue(conversationId);
      toast.success("已加入重新生成队列");
    },
    [runConversationQueue, updateConversation],
  );

  const handleSaveEditingTurn = useCallback(
    async (regenerate: boolean) => {
      const draft = editingTurnDraft;
      if (!draft) {
        return;
      }
      const prompt = draft.prompt.trim();
      if (!prompt) {
        toast.error("请输入提示词");
        return;
      }

      const targetConversation = conversationsRef.current.find((conversation) => conversation.id === draft.conversationId);
      const targetTurn = targetConversation?.turns.find((turn) => turn.id === draft.turnId);
      if (!targetConversation || !targetTurn) {
        toast.error("未找到对应的对话轮次");
        return;
      }
      if (isTurnInProgress(targetTurn)) {
        toast.error("当前轮次正在处理，稍后再编辑");
        return;
      }

      const imageCount = draft.mode === "chat" ? 1 : normalizeRequestedImageCount(draft.count);
      const mode = draft.mode === "chat" ? "chat" : getComposerConversationMode("image", draft.referenceImages);
      const referenceImages = mode === "chat" ? draft.referenceImages : usesReferenceImages(mode) ? draft.referenceImages : [];
      if (mode !== "chat" && isInvalidCustomRatioSelection(draft.sizeMode, draft.aspectRatio, draft.customRatio)) {
        toast.error("请输入有效的自定义比例，例如 5:4 或 2.39:1");
        return;
      }
      const draftImageSize =
        mode === "chat"
          ? ""
          : buildImageSize({
              mode: draft.sizeMode,
              aspectRatio: draft.aspectRatio,
              resolution: draft.resolution,
              customRatio: draft.customRatio,
              customWidth: draft.customWidth,
              customHeight: draft.customHeight,
            });
      const draftSizeSelection = serializeImageSizeSelection({
        mode: draft.sizeMode,
        aspectRatio: draft.aspectRatio,
        resolution: draft.resolution,
        customRatio: draft.customRatio,
        customWidth: draft.customWidth,
        customHeight: draft.customHeight,
      });
      if (mode !== "chat" && draft.sizeMode === "custom" && !draftImageSize) {
        toast.error("请填写有效的宽度和高度");
        return;
      }
      const draftOutputCompression = imageOutputCompressionForFormat(draft.outputFormat, draft.outputCompression);
      const now = new Date().toISOString();
      const regenerationId = createId();
      await updateConversation(draft.conversationId, (current) => {
        const conversation = current ?? targetConversation;
        const isFirstTurn = conversation.turns[0]?.id === draft.turnId;
        return {
          ...conversation,
          title: isFirstTurn ? buildConversationTitle(prompt) : conversation.title,
          updatedAt: now,
          turns: conversation.turns.map((turn) => {
            if (turn.id !== draft.turnId) {
              return turn;
            }

            const baseTurn = {
              ...turn,
              prompt,
              model: draft.model,
              mode,
              referenceImages,
              count: imageCount,
              size: draftImageSize,
              sizeSelection: mode === "chat" ? undefined : draftSizeSelection,
              quality: mode === "chat" ? undefined : imageQualityForModel(draft.model, draft.quality),
              outputFormat: mode === "chat" ? undefined : draft.outputFormat,
              outputCompression: mode === "chat" ? undefined : draftOutputCompression,
              visibility: mode === "chat" ? "private" : draft.visibility,
            };
            if (!regenerate) {
              return baseTurn;
            }
            return {
              ...baseTurn,
              status: "queued" as const,
              error: undefined,
              images: Array.from({ length: imageCount }, (_, index) => {
                const imageId = `${turn.id}-${regenerationId}-${index}`;
                return {
                  id: imageId,
                  taskId: imageTaskBatchId(`${turn.id}-${regenerationId}`, index),
                  status: "loading" as const,
                  visibility: baseTurn.mode === "chat" ? undefined : baseTurn.visibility,
                };
              }),
            };
          }),
        };
      });

      setEditingTurnDraft(null);
      if (editFileInputRef.current) {
        editFileInputRef.current.value = "";
      }
      if (regenerate) {
        void runConversationQueue(draft.conversationId);
        toast.success("已保存并加入重新生成队列");
      } else {
        toast.success("已保存编辑设置");
      }
    },
    [editingTurnDraft, runConversationQueue, updateConversation],
  );

  const handleSubmit = async () => {
    if (isSubmitDispatchingRef.current) {
      return;
    }

    const prompt = imagePrompt.trim();
    if (!prompt) {
      toast.error("请输入提示词");
      return;
    }
    if (composerMode === "image" && imageSizeMode === "custom" && !imageSize) {
      toast.error("请填写有效的宽度和高度");
      return;
    }
    if (composerMode === "image" && isInvalidCustomRatioSelection(imageSizeMode, imageAspectRatio, imageCustomRatio)) {
      toast.error("请输入有效的自定义比例，例如 5:4 或 2.39:1");
      return;
    }
    isSubmitDispatchingRef.current = true;
    let draftProgressTarget: { conversationId: string; turnId: string } | null = null;

    try {
      const effectiveImageMode = getComposerConversationMode(composerMode, referenceImages);
      const effectiveModel =
        effectiveImageMode === "chat"
          ? isChatModel(imageModel)
            ? imageModel
            : DEFAULT_CHAT_MODEL
          : isImageCreationModel(imageModel)
            ? imageModel
            : DEFAULT_IMAGE_MODEL;
      const requestedCount = effectiveImageMode === "chat" ? 1 : parsedCount;
      const effectiveOutputCompression = imageOutputCompressionForFormat(imageOutputFormat, imageOutputCompression);
      const currentImageSizeSelection = serializeImageSizeSelection({
        mode: imageSizeMode,
        aspectRatio: imageAspectRatio,
        resolution: imageResolution,
        customRatio: imageCustomRatio,
        customWidth: imageCustomWidth,
        customHeight: imageCustomHeight,
      });

      const targetConversation = selectedConversationId
        ? conversationsRef.current.find((conversation) => conversation.id === selectedConversationId) ?? null
        : null;
      const now = new Date().toISOString();
      const conversationId = targetConversation?.id ?? createId();
      const turnId = createId();
      const draftTurn: ImageTurn = {
        id: turnId,
        prompt,
        model: effectiveModel,
        mode: effectiveImageMode,
        referenceImages: effectiveImageMode === "chat" ? referenceImages : usesReferenceImages(effectiveImageMode) ? referenceImages : [],
        count: requestedCount,
        size: effectiveImageMode === "chat" ? "" : imageSize,
        sizeSelection: effectiveImageMode === "chat" ? undefined : currentImageSizeSelection,
        quality: effectiveImageMode === "chat" ? undefined : imageQualityForModel(effectiveModel, imageQuality),
        outputFormat: effectiveImageMode === "chat" ? undefined : imageOutputFormat,
        outputCompression: effectiveImageMode === "chat" ? undefined : effectiveOutputCompression,
        visibility: effectiveImageMode === "chat" ? "private" : defaultImageVisibility,
        images: Array.from({ length: requestedCount }, (_, index) => {
          const imageId = `${turnId}-${index}`;
          return {
            id: imageId,
            taskId: imageTaskBatchId(turnId, index),
            status: "loading" as const,
            visibility: effectiveImageMode === "chat" ? undefined : defaultImageVisibility,
          };
        }),
        createdAt: now,
        status: "queued",
      };

      const baseConversation: ImageConversation = targetConversation
        ? {
            ...targetConversation,
            updatedAt: now,
            turns: [...targetConversation.turns, draftTurn],
          }
        : {
            id: conversationId,
            title: buildConversationTitle(prompt),
            createdAt: now,
            updatedAt: now,
            turns: [draftTurn],
          };

      draftProgressTarget = { conversationId, turnId };
      updateTurnProgress(conversationId, turnId, {
        message: "正在创建本地记录",
        detail: effectiveImageMode === "chat" ? "正在保存对话内容" : "正在保存提示词和生成参数",
      });
      setSelectedConversationId(conversationId);
      clearComposerInputs();

      await persistConversation(baseConversation);
      void runConversationQueue(conversationId);

      const targetStats = getImageConversationStats(baseConversation);
      if (targetStats.running > 0 || targetStats.queued > 1) {
        toast.success("已加入当前对话队列");
      } else if (!targetConversation) {
        toast.success(effectiveImageMode === "chat" ? "已创建新对话并发送" : "已创建新对话并开始处理");
      } else {
        toast.success("已发送到当前对话");
      }
    } catch (error) {
      if (draftProgressTarget) {
        clearTurnProgress(draftProgressTarget.conversationId, draftProgressTarget.turnId);
      }
      toast.error(formatCreationTaskError(error, "提交任务失败"));
    } finally {
      isSubmitDispatchingRef.current = false;
    }
  };

  return (
    <>
      <section className="relative isolate mx-auto grid h-[calc(100dvh-5.65rem)] min-h-0 w-full max-w-[1500px] grid-cols-1 gap-2 overflow-hidden rounded-[26px] border border-white/80 bg-[radial-gradient(circle_at_34%_0%,rgba(59,91,255,0.12),transparent_34%),linear-gradient(180deg,rgba(255,255,255,0.86)_0%,rgba(246,249,255,0.68)_50%,rgba(241,246,252,0.92)_100%)] px-0 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] shadow-[0_20px_80px_-56px_rgba(15,23,42,0.52)] dark:border-[#b58a3a]/35 dark:bg-[radial-gradient(circle_at_30%_0%,rgba(255,204,112,0.18),transparent_34%),radial-gradient(circle_at_86%_18%,rgba(190,135,44,0.16),transparent_28%),linear-gradient(180deg,#090806_0%,#14100a_48%,#070604_100%)] dark:shadow-[0_24px_110px_-56px_rgba(0,0,0,0.95),inset_0_1px_0_rgba(255,221,145,0.16)] sm:h-[calc(100dvh-4.7rem)] sm:gap-2.5 sm:px-2 sm:py-2 sm:pb-4 lg:grid-cols-[220px_minmax(0,1fr)]">
        <div className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-44 bg-[linear-gradient(116deg,rgba(255,255,255,0.72),rgba(219,234,254,0.2),rgba(255,255,255,0))] dark:bg-[linear-gradient(116deg,rgba(255,224,166,0.20),rgba(181,138,58,0.08),rgba(255,255,255,0))]" />
        {canRenderDesktopSidebar ? (
          <div className="hidden h-full min-h-0 rounded-[22px] border border-white/70 bg-white/[0.52] px-2 py-2 shadow-[inset_0_1px_0_rgba(255,255,255,0.7)] backdrop-blur-xl dark:border-[#d6aa56]/24 dark:bg-[#0e0d0a]/82 dark:shadow-[inset_0_1px_0_rgba(255,226,166,0.12),0_24px_70px_-46px_rgba(0,0,0,0.95)] lg:block">
            <Suspense fallback={<ImageSidebarFallback />}>
              <ImageSidebar
                conversations={conversations}
                isLoadingHistory={isLoadingHistory}
                selectedConversationId={selectedConversationId}
                onCreateDraft={handleCreateDraft}
                onClearHistory={openClearHistoryConfirm}
                onSelectConversation={setSelectedConversationId}
                onDeleteConversation={openDeleteConversationConfirm}
                formatConversationTime={formatConversationTime}
              />
            </Suspense>
          </div>
        ) : null}

        <Dialog open={isHistoryOpen} onOpenChange={setIsHistoryOpen}>
          <DialogContent className="flex h-[min(82dvh,760px)] w-[92vw] max-w-[460px] flex-col overflow-hidden rounded-[32px] border-white/80 bg-white p-0 shadow-[0_32px_110px_-38px_rgba(15,23,42,0.45)] dark:border-[#d6aa56]/28 dark:bg-[#0e0d0a] sm:rounded-[36px]">
            <DialogHeader className="px-6 pt-7 pb-4 sm:px-8">
              <DialogTitle className="flex items-center gap-2 text-xl font-bold tracking-tight">
                <History className="size-5" />
                历史记录
              </DialogTitle>
            </DialogHeader>
            <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-8 sm:px-8">
              <Suspense fallback={<ImageSidebarFallback compact />}>
                <ImageSidebar
                  conversations={conversations}
                  isLoadingHistory={isLoadingHistory}
                  selectedConversationId={selectedConversationId}
                  onCreateDraft={() => {
                    handleCreateDraft();
                    setIsHistoryOpen(false);
                  }}
                  onClearHistory={openClearHistoryConfirm}
                  onSelectConversation={(id) => {
                    setSelectedConversationId(id);
                    setIsHistoryOpen(false);
                  }}
                  onDeleteConversation={openDeleteConversationConfirm}
                  formatConversationTime={formatConversationTime}
                  hideActionButtons
                  initialVisibleCount={20}
                  loadMoreCount={20}
                />
              </Suspense>
            </div>
          </DialogContent>
        </Dialog>

        {editingTurnDraft ? (
          <Suspense fallback={<LazyDialogFallback label="正在打开编辑面板..." />}>
            <ImageTurnEditDialog
              draft={editingTurnDraft}
              customRatioInvalid={editingDraftCustomRatioInvalid}
              sizePreviewLabel={editingDraftSizePreviewLabel}
              sizePreviewDetail={editingDraftSizePreviewDetail}
              fileInputRef={editFileInputRef}
              onDraftChange={setEditingTurnDraft}
              onReferenceImageChange={handleEditReferenceImageChange}
              onRemoveReferenceImage={handleRemoveEditReferenceImage}
              onOpenLightbox={openLightbox}
              onSave={handleSaveEditingTurn}
              onClose={() => setEditingTurnDraft(null)}
            />
          </Suspense>
        ) : null}

        <div className="relative flex min-h-0 flex-col gap-1.5 sm:gap-2">
          <div className="flex items-center justify-between gap-2 px-1 sm:px-2">
            <div className="flex min-w-0 flex-1 items-center gap-2 lg:hidden">
              <Button
                variant="outline"
                className="h-10 min-w-0 flex-1 shrink rounded-full border-[#e5e7eb] bg-white text-[#45515e] shadow-sm"
                onClick={() => setIsHistoryOpen(true)}
              >
                <History className="size-4" />
                <span className="truncate">历史记录 ({conversations.length})</span>
              </Button>
              <Button
                className="h-10 rounded-full shadow-sm"
                onClick={handleCreateDraft}
              >
                <Plus className="size-4" />
                新建
              </Button>
              <Button
                variant="outline"
                className="h-10 rounded-full border-[#e5e7eb] bg-white px-3 text-[#45515e] shadow-sm"
                onClick={openClearHistoryConfirm}
                disabled={conversations.length === 0}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
          </div>

          <div
            ref={resultsViewportRef}
            className="hide-scrollbar min-h-0 flex-1 overflow-y-auto px-1 pt-1.5 pb-[10.5rem] sm:px-3 sm:pt-2.5 sm:pb-[14rem]"
            style={composerDockHeight > 0 ? { paddingBottom: composerDockHeight + 18 } : undefined}
          >
            <Suspense
              fallback={
                <div className="flex min-h-[180px] items-center justify-center rounded-[24px] border border-white/70 bg-white/55 text-sm font-semibold text-[#45515e] backdrop-blur-xl dark:border-[#d6aa56]/20 dark:bg-[#0e0d0a]/70 dark:text-[#d6aa56]">
                  正在打开创作结果区...
                </div>
              }
            >
              <ImageResults
                selectedConversation={selectedConversation}
                progressByTurnKey={progressByTurnKey}
                progressNow={progressNow}
                promptPresets={publicPromptPresets}
                isLoadingPromptPresets={isLoadingPublicPromptPresets}
                promptPresetStatus={publicPromptPresetStatus}
                onOpenLightbox={openLightbox}
                onApplyPromptPreset={handleApplyPromptPreset}
                onContinueEdit={handleContinueEdit}
                onEditTurn={openEditTurnDialog}
                onCancelTurn={handleCancelTurn}
                onRegenerateTurn={handleRegenerateTurn}
                onRetryImage={handleRetryImage}
                onImageVisibilityChange={handleImageVisibilityChange}
                visibilityMutatingImageKey={visibilityMutatingImageKey}
                formatConversationTime={formatConversationTime}
              />
            </Suspense>
          </div>

          <div
            ref={composerDockRef}
            className="pointer-events-none absolute inset-x-0 bottom-0 z-30 px-1 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] sm:px-3 sm:pb-2"
            style={
              {
                "--image-composer-dock-height": `${composerDockHeight}px`,
              } as CSSProperties
            }
          >
            <div className="pointer-events-auto mx-auto w-full max-w-[960px]">
              <ImageComposer
                composerMode={composerMode}
                prompt={imagePrompt}
                imageCount={imageCount}
                imageModel={imageModel}
                imageModelOptions={composerModelOptions}
                imageSizeMode={imageSizeMode}
                imageAspectRatio={imageAspectRatio}
                imageResolution={imageResolution}
                imageCustomRatio={imageCustomRatio}
                imageCustomWidth={imageCustomWidth}
                imageCustomHeight={imageCustomHeight}
                imageQuality={imageQuality}
                imageQualityOptions={imageQualityOptions}
                imageOutputFormat={imageOutputFormat}
                imageOutputCompression={imageOutputCompression}
                imageOutputHint={imageOutputHint}
                referenceImages={referenceImages}
                textareaRef={textareaRef}
                fileInputRef={fileInputRef}
                onComposerModeChange={handleComposerModeChange}
                onPromptChange={setImagePrompt}
                onImageCountChange={setImageCount}
                onImageModelChange={setImageModel}
                onImageSizeModeChange={setImageSizeMode}
                onImageAspectRatioChange={setImageAspectRatio}
                onImageResolutionChange={setImageResolution}
                onImageCustomRatioChange={setImageCustomRatio}
                onImageCustomWidthChange={setImageCustomWidth}
                onImageCustomHeightChange={setImageCustomHeight}
                onImageQualityChange={setImageQuality}
                onImageOutputFormatChange={setImageOutputFormat}
                onImageOutputCompressionChange={setImageOutputCompression}
                onSubmit={handleSubmit}
                onOpenPromptMarket={() => setIsPromptMarketOpen(true)}
                onReferenceImageChange={handleReferenceImageChange}
                onRemoveReferenceImage={handleRemoveReferenceImage}
              />
            </div>
          </div>
        </div>
      </section>

      {isPromptMarketOpen ? (
        <Suspense fallback={<LazyDialogFallback label="正在打开提示词市场..." />}>
          <ImagePromptMarket
            open={isPromptMarketOpen}
            onOpenChange={setIsPromptMarketOpen}
            onApplyPrompt={handleApplyMarketPrompt}
            isAdmin={isAdmin}
          />
        </Suspense>
      ) : null}

      {lightboxOpen ? (
        <Suspense fallback={<LazyDialogFallback label="正在打开图片预览..." />}>
          <ImageLightbox
            images={lightboxImages}
            currentIndex={lightboxIndex}
            open={lightboxOpen}
            onOpenChange={setLightboxOpen}
            onIndexChange={setLightboxIndex}
          />
        </Suspense>
      ) : null}

      {deleteConfirm ? (
        <Dialog open onOpenChange={(open) => (!open ? setDeleteConfirm(null) : null)}>
          <DialogContent showCloseButton={false} className="rounded-2xl p-6">
            <DialogHeader className="gap-2">
              <DialogTitle>{deleteConfirmTitle}</DialogTitle>
              <DialogDescription className="text-sm leading-6">
                {deleteConfirmDescription}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="outline" onClick={() => setDeleteConfirm(null)}>
                取消
              </Button>
              <Button className="bg-rose-600 text-white hover:bg-rose-700" onClick={() => void handleConfirmDelete()}>
                确认删除
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </>
  );
}

export default function ImagePage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/image");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-stone-400" />
      </div>
    );
  }

  return <ImagePageContent isAdmin={session.role === "admin"} />;
}
