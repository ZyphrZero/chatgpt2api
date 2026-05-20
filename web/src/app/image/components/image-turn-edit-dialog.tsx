"use client";

import { ImagePlus, X } from "lucide-react";
import type { Dispatch, RefObject, SetStateAction } from "react";

import {
  CUSTOM_IMAGE_ASPECT_RATIO,
  DEFAULT_IMAGE_CUSTOM_HEIGHT,
  DEFAULT_IMAGE_CUSTOM_RATIO,
  DEFAULT_IMAGE_CUSTOM_WIDTH,
  IMAGE_ASPECT_RATIO_OPTIONS,
  IMAGE_QUALITY_OPTIONS,
  IMAGE_RESOLUTION_OPTIONS,
  IMAGE_SIZE_MODE_OPTIONS,
  isImageAspectRatio,
  isImageResolution,
  isImageSizeMode,
  type ImageAspectRatio,
  type ImageResolution,
  type ImageSizeMode,
} from "@/app/image/image-options";
import type { ImageLightboxItem } from "@/app/image/components/image-results";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  CHAT_MODEL_OPTIONS,
  IMAGE_CREATION_MODEL_OPTIONS,
  IMAGE_OUTPUT_FORMAT_OPTIONS,
  isImageModel,
  isImageOutputFormat,
  isImageQuality,
  supportsImageQuality,
  type ImageModel,
  type ImageOutputFormat,
  type ImageQuality,
  type ImageVisibility,
} from "@/lib/api";
import { cn } from "@/lib/utils";
import type { ImageConversationMode, StoredReferenceImage } from "@/store/image-conversations";

export type ImageTurnEditDraft = {
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

type ImageTurnEditDialogProps = {
  draft: ImageTurnEditDraft;
  customRatioInvalid: boolean;
  sizePreviewLabel: string;
  sizePreviewDetail: string;
  fileInputRef: RefObject<HTMLInputElement | null>;
  onDraftChange: Dispatch<SetStateAction<ImageTurnEditDraft | null>>;
  onReferenceImageChange: (files: File[]) => void | Promise<void>;
  onRemoveReferenceImage: (index: number) => void;
  onOpenLightbox: (images: ImageLightboxItem[], index: number) => void;
  onSave: (regenerate: boolean) => void | Promise<void>;
  onClose: () => void;
};

const EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE = "__empty_aspect_ratio__";

export function ImageTurnEditDialog({
  draft,
  customRatioInvalid,
  sizePreviewLabel,
  sizePreviewDetail,
  fileInputRef,
  onDraftChange,
  onReferenceImageChange,
  onRemoveReferenceImage,
  onOpenLightbox,
  onSave,
  onClose,
}: ImageTurnEditDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => (!open ? onClose() : null)}>
      <DialogContent className="flex max-h-[88dvh] w-[min(92vw,640px)] flex-col overflow-hidden rounded-[28px] p-0">
        <DialogHeader className="px-6 pt-6 pb-2">
          <DialogTitle>{draft.mode === "chat" ? "编辑对话" : "编辑生成设置"}</DialogTitle>
          <DialogDescription>
            {draft.mode === "chat" ? "修改本轮消息和对话模型。" : "修改本轮提示词、参考图和生成参数。"}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          <div className="flex flex-col gap-5">
            <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
              提示词
              <Textarea
                value={draft.prompt}
                onChange={(event) =>
                  onDraftChange((current) =>
                    current ? { ...current, prompt: event.target.value } : current,
                  )
                }
                className="min-h-[128px] resize-y rounded-2xl border-stone-200 bg-white text-sm leading-6 shadow-none"
              />
            </label>

            {draft.mode !== "chat" ? (
              <div className="flex flex-col gap-3">
                <input
                  ref={fileInputRef}
                  type="file"
                  accept="image/*"
                  multiple
                  className="hidden"
                  onChange={(event) => {
                    void onReferenceImageChange(Array.from(event.target.files || []));
                  }}
                />
                <div className="flex items-center justify-between gap-3">
                  <div className="text-sm font-medium text-stone-700">参考图</div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="rounded-full border-stone-200 bg-white"
                    onClick={() => fileInputRef.current?.click()}
                  >
                    <ImagePlus className="size-4" />
                    上传图片
                  </Button>
                </div>
                {draft.referenceImages.length > 0 ? (
                  <div className="flex flex-wrap gap-2">
                    {draft.referenceImages.map((image, index) => (
                      <div key={`${image.name}-${index}`} className="relative size-20 shrink-0">
                        <button
                          type="button"
                          className="size-20 overflow-hidden rounded-2xl border border-stone-200 bg-stone-100"
                          onClick={() =>
                            onOpenLightbox(
                              draft.referenceImages.map((item, itemIndex) => ({
                                id: `${item.name}-${itemIndex}`,
                                src: item.dataUrl,
                              })),
                              index,
                            )
                          }
                          aria-label={`预览参考图 ${image.name || index + 1}`}
                        >
                          <img
                            src={image.dataUrl}
                            alt={image.name || `参考图 ${index + 1}`}
                            className="h-full w-full object-cover"
                          />
                        </button>
                        <button
                          type="button"
                          onClick={() => onRemoveReferenceImage(index)}
                          className="absolute -top-1 -right-1 z-10 inline-flex size-6 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 shadow-sm transition hover:text-stone-900"
                          aria-label={`移除参考图 ${image.name || index + 1}`}
                        >
                          <X className="size-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : null}

            <div className={cn("grid grid-cols-1 gap-3", draft.mode === "chat" ? "sm:grid-cols-1" : "sm:grid-cols-2 lg:grid-cols-4")}>
              {draft.mode !== "chat" ? (
                <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                  张数
                  <Input
                    type="number"
                    inputMode="numeric"
                    min="1"
                    max="10"
                    step="1"
                    value={draft.count}
                    onChange={(event) =>
                      onDraftChange((current) =>
                        current ? { ...current, count: event.target.value } : current,
                      )
                    }
                  />
                </label>
              ) : null}
              <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                模型
                <Select
                  value={draft.model}
                  onValueChange={(value) =>
                    onDraftChange((current) =>
                      current && isImageModel(value) ? { ...current, model: value } : current,
                    )
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {(draft.mode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_CREATION_MODEL_OPTIONS).map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </label>
              {draft.mode !== "chat" ? (
                <>
                  <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                    尺寸
                    <Select
                      value={draft.sizeMode}
                      onValueChange={(value) =>
                        onDraftChange((current) =>
                          current && isImageSizeMode(value) ? { ...current, sizeMode: value } : current,
                        )
                      }
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {IMAGE_SIZE_MODE_OPTIONS.map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </label>
                  {draft.sizeMode === "custom" ? (
                    <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-end gap-2 lg:col-span-2">
                      <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                        宽度
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          step="1"
                          value={draft.customWidth}
                          onChange={(event) =>
                            onDraftChange((current) =>
                              current ? { ...current, customWidth: event.target.value } : current,
                            )
                          }
                        />
                      </label>
                      <span className="pb-2 text-sm font-medium text-stone-400">x</span>
                      <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                        高度
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          step="1"
                          value={draft.customHeight}
                          onChange={(event) =>
                            onDraftChange((current) =>
                              current ? { ...current, customHeight: event.target.value } : current,
                            )
                          }
                        />
                      </label>
                    </div>
                  ) : null}
                  {draft.sizeMode === "ratio" ? (
                    <>
                      <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                        比例
                        <Select
                          value={draft.aspectRatio || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                          onValueChange={(value) =>
                            onDraftChange((current) =>
                              current
                                ? {
                                    ...current,
                                    aspectRatio:
                                      value === EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE
                                        ? ""
                                        : isImageAspectRatio(value)
                                          ? value
                                          : current.aspectRatio,
                                  }
                                : current,
                            )
                          }
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {IMAGE_ASPECT_RATIO_OPTIONS.map((option) => (
                                <SelectItem
                                  key={option.label}
                                  value={option.value || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                                >
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </label>
                      <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                        分辨率
                        <Select
                          value={draft.resolution}
                          onValueChange={(value) =>
                            onDraftChange((current) =>
                              current && isImageResolution(value) ? { ...current, resolution: value } : current,
                            )
                          }
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {IMAGE_RESOLUTION_OPTIONS.map((option) => (
                                <SelectItem key={option.value} value={option.value}>
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </label>
                      {draft.aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO ? (
                        <label className="flex flex-col gap-2 text-sm font-medium text-stone-700 sm:col-span-2">
                          自定义比例
                          <Input
                            value={draft.customRatio}
                            onChange={(event) =>
                              onDraftChange((current) =>
                                current ? { ...current, customRatio: event.target.value } : current,
                              )
                            }
                            placeholder="例如 5:4 / 2.39:1"
                            aria-invalid={customRatioInvalid}
                            className={cn(customRatioInvalid && "border-red-300 focus-visible:ring-red-500/20")}
                          />
                        </label>
                      ) : null}
                    </>
                  ) : null}
                  <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                    格式
                    <Select
                      value={draft.outputFormat}
                      onValueChange={(value) =>
                        onDraftChange((current) =>
                          current && isImageOutputFormat(value)
                            ? { ...current, outputFormat: value, outputCompression: value === "png" ? "" : current.outputCompression }
                            : current,
                        )
                      }
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {IMAGE_OUTPUT_FORMAT_OPTIONS.map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </label>
                  <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                    压缩率
                    <Input
                      type="number"
                      inputMode="numeric"
                      min="0"
                      max="100"
                      step="1"
                      value={draft.outputCompression}
                      disabled={draft.outputFormat === "png"}
                      onChange={(event) =>
                        onDraftChange((current) =>
                          current ? { ...current, outputCompression: event.target.value } : current,
                        )
                      }
                      placeholder={draft.outputFormat === "png" ? "PNG 不适用" : "0-100"}
                    />
                  </label>
                  {draft.sizeMode !== "auto" ? (
                    <div className="rounded-2xl border border-stone-200 bg-stone-50 px-3 py-2 text-sm sm:col-span-2 lg:col-span-4">
                      <div className="flex min-w-0 items-center justify-between gap-3">
                        <span className="shrink-0 font-medium text-stone-600">计算后分辨率</span>
                        <span className="min-w-0 truncate text-right font-mono font-semibold text-stone-900">
                          {sizePreviewLabel}
                        </span>
                      </div>
                      <div className="mt-1 truncate text-xs text-stone-500">{sizePreviewDetail}</div>
                    </div>
                  ) : null}
                  {supportsImageQuality(draft.model) ? (
                    <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                      质量
                      <Select
                        value={draft.quality}
                        onValueChange={(value) =>
                          onDraftChange((current) =>
                            current && isImageQuality(value) ? { ...current, quality: value } : current,
                          )
                        }
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            {IMAGE_QUALITY_OPTIONS.map((option) => (
                              <SelectItem key={option.value} value={option.value}>
                                {option.label}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </label>
                  ) : null}
                </>
              ) : null}
            </div>
          </div>
        </div>
        <DialogFooter className="border-t border-stone-100 px-6 py-4">
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button variant="outline" onClick={() => void onSave(false)}>
            保存
          </Button>
          <Button onClick={() => void onSave(true)}>
            {draft.mode === "chat" ? "保存并重新发送" : "保存并重新生成"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
