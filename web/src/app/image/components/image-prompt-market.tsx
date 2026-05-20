"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ExternalLink, LoaderCircle, RefreshCcw, Search, SlidersHorizontal, Star } from "lucide-react";
import { toast } from "sonner";

import {
  type BananaPrompt,
  type BananaPromptMode,
  type PromptMarketLanguage,
  type PromptMarketLocalization,
  type PromptMarketSourceId,
} from "@/app/image/banana-prompts";
import {
  createPromptFavorite,
  deletePromptFavorite,
  fetchPromptFavorites,
  promptFavoriteKey,
  promptFavoriteRecordKey,
  promptFavoriteToBananaPrompt,
  type PromptFavorite,
} from "@/app/image/prompt-favorites";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
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
import { fetchManagedImages, type ManagedImage } from "@/lib/api";
import { cn } from "@/lib/utils";

type PromptMarketModeFilter = "all" | BananaPromptMode;
type PromptMarketNsfwFilter = "safe" | "include" | "only";
type PromptMarketFavoriteFilter = "all" | "favorites";

type ImagePromptMarketProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onApplyPrompt: (prompt: BananaPrompt) => void | Promise<void>;
  isAdmin?: boolean;
};

const ALL_CATEGORY_VALUE = "__all__";
const INITIAL_VISIBLE_COUNT = 60;
const VISIBLE_COUNT_STEP = 60;

function includesKeyword(value: string | undefined, keyword: string) {
  return Boolean(value && value.toLowerCase().includes(keyword));
}

function formatPromptDate(value?: string) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

function getPromptLocalization(
  prompt: BananaPrompt,
  language: PromptMarketLanguage,
): PromptMarketLocalization | undefined {
  return prompt.localizations?.[language] ?? prompt.localizations?.["zh-CN"] ?? prompt.localizations?.en;
}

function getLocalizedPrompt(prompt: BananaPrompt, language: PromptMarketLanguage): BananaPrompt {
  const localization = getPromptLocalization(prompt, language);
  if (!localization) {
    return prompt;
  }

  return {
    ...prompt,
    title: localization.title,
    prompt: localization.prompt,
    category: localization.category,
    subCategory: localization.subCategory,
  };
}

function PromptPreviewImage({ prompt }: { prompt: BananaPrompt }) {
  const [failed, setFailed] = useState(false);

  if (failed) {
    return (
      <div className="absolute inset-0 flex items-center justify-center px-4 text-center text-sm font-medium text-[#8e8e93]">
        {prompt.title}
      </div>
    );
  }

  return (
    <img
      src={prompt.preview}
      alt={prompt.title}
      loading="lazy"
      className="h-full w-full object-cover transition duration-300 group-hover:scale-[1.03]"
      onError={() => setFailed(true)}
    />
  );
}

function imagePromptFromPublicImage(item: ManagedImage): BananaPrompt | null {
  const prompt = (item.prompt || "").trim();
  if (!prompt) {
    return null;
  }
  const title = prompt ? prompt.slice(0, 22) : item.name.replace(/\.[^.]+$/, "") || "公开图库模板";
  return {
    id: `public-image-gallery:${item.path}`,
    title,
    preview: item.thumbnail_url || item.url,
    referenceImageUrls: [item.url],
    prompt,
    author: item.owner_name || "公开图库",
    link: item.url,
    mode: "edit",
    category: "公开图库",
    subCategory: item.resolution_preset || item.aspect_ratio || item.orientation || undefined,
    created: item.published_at || item.created_at,
    source: "public-image-gallery",
    sourceLabel: "公开图库",
    isNsfw: false,
  };
}

export function ImagePromptMarket({ open, onOpenChange, onApplyPrompt, isAdmin = false }: ImagePromptMarketProps) {
  const [prompts, setPrompts] = useState<BananaPrompt[]>([]);
  const [favoriteItems, setFavoriteItems] = useState<PromptFavorite[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [isLoadingFavorites, setIsLoadingFavorites] = useState(false);
  const [error, setError] = useState("");
  const [favoriteError, setFavoriteError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [favoriteFilter, setFavoriteFilter] = useState<PromptMarketFavoriteFilter>("all");
  const [promptLanguage, setPromptLanguage] = useState<PromptMarketLanguage>("zh-CN");
  const [category, setCategory] = useState(ALL_CATEGORY_VALUE);
  const [mode, setMode] = useState<PromptMarketModeFilter>("all");
  const [nsfwFilter, setNsfwFilter] = useState<PromptMarketNsfwFilter>("safe");
  const [isMobileFiltersOpen, setIsMobileFiltersOpen] = useState(false);
  const [visibleCount, setVisibleCount] = useState(INITIAL_VISIBLE_COUNT);
  const [favoriteBusyIds, setFavoriteBusyIds] = useState<Set<string>>(() => new Set());
  const scrollAreaRef = useRef<HTMLDivElement>(null);

  const updateFavoriteItems = (items: PromptFavorite[]) => {
    setFavoriteItems(Array.isArray(items) ? items : []);
  };

  const loadPromptData = () => {
    setIsLoading(true);
    setError("");

    void fetchManagedImages({ scope: "public" })
      .then((items) => {
        setPrompts(items.items.map(imagePromptFromPublicImage).filter((item): item is BananaPrompt => Boolean(item)));
      })
      .catch((loadError: unknown) => {
        setError(loadError instanceof Error ? loadError.message : "读取公开图库失败");
      })
      .finally(() => {
        setIsLoading(false);
      });
  };

  const loadFavoriteData = () => {
    setIsLoadingFavorites(true);
    setFavoriteError("");

    void fetchPromptFavorites()
      .then((data) => {
        updateFavoriteItems(data.items);
      })
      .catch((loadError: unknown) => {
        const message = loadError instanceof Error ? loadError.message : "读取收藏失败";
        setFavoriteError(message);
        toast.error(message);
      })
      .finally(() => {
        setIsLoadingFavorites(false);
      });
  };

  useEffect(() => {
    if (!open || prompts.length > 0) {
      return;
    }

    setIsLoading(true);
    setError("");
    const controller = new AbortController();

    void fetchManagedImages({ scope: "public" }, { signal: controller.signal })
      .then((items) => {
        setPrompts(items.items.map(imagePromptFromPublicImage).filter((item): item is BananaPrompt => Boolean(item)));
      })
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setError(loadError instanceof Error ? loadError.message : "读取公开图库失败");
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setIsLoading(false);
        }
      });

    return () => controller.abort();
  }, [open, prompts.length]);

  useEffect(() => {
    if (!open || favoriteItems.length > 0) {
      return;
    }

    setIsLoadingFavorites(true);
    setFavoriteError("");
    const controller = new AbortController();

    void fetchPromptFavorites(controller.signal)
      .then((data) => {
        updateFavoriteItems(data.items);
      })
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        const message = loadError instanceof Error ? loadError.message : "读取收藏失败";
        setFavoriteError(message);
        toast.error(message);
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setIsLoadingFavorites(false);
        }
      });

    return () => controller.abort();
  }, [favoriteItems.length, open]);

  useEffect(() => {
    setVisibleCount(INITIAL_VISIBLE_COUNT);
    scrollAreaRef.current?.scrollTo({ top: 0 });
  }, [keyword, promptLanguage, category, mode, nsfwFilter, favoriteFilter]);

  useEffect(() => {
    if (!isAdmin && nsfwFilter !== "safe") {
      setNsfwFilter("safe");
    }
  }, [isAdmin, nsfwFilter]);

  useEffect(() => {
    if (open) {
      scrollAreaRef.current?.scrollTo({ top: 0 });
      return;
    }
    setIsMobileFiltersOpen(false);
  }, [open]);

  const favoritePrompts = useMemo(
    () => favoriteItems.map((item) => promptFavoriteToBananaPrompt(item)),
    [favoriteItems],
  );

  const favoriteIds = useMemo(() => new Set(favoriteItems.map((item) => promptFavoriteRecordKey(item))), [favoriteItems]);

  const favoriteByPromptKey = useMemo(() => {
    const items = new Map<string, PromptFavorite>();
    favoriteItems.forEach((item) => {
      items.set(promptFavoriteRecordKey(item), item);
    });
    return items;
  }, [favoriteItems]);

  const promptPool = favoriteFilter === "favorites" ? favoritePrompts : prompts;

  const categories = useMemo(() => {
    const values = new Set<string>();
    promptPool.forEach((prompt) => {
      values.add(getLocalizedPrompt(prompt, promptLanguage).category);
    });
    return [...values].sort((a, b) => a.localeCompare(b, "zh-CN"));
  }, [promptLanguage, promptPool]);

  useEffect(() => {
    if (category !== ALL_CATEGORY_VALUE && !categories.includes(category)) {
      setCategory(ALL_CATEGORY_VALUE);
    }
  }, [categories, category]);

  const filteredPrompts = useMemo(() => {
    const normalizedKeyword = keyword.trim().toLowerCase();

    return promptPool.filter((prompt) => {
      const localizedPrompt = getLocalizedPrompt(prompt, promptLanguage);
      if (nsfwFilter === "safe" && prompt.isNsfw) {
        return false;
      }
      if (nsfwFilter === "only" && !prompt.isNsfw) {
        return false;
      }
      if (category !== ALL_CATEGORY_VALUE && localizedPrompt.category !== category) {
        return false;
      }
      if (mode !== "all" && localizedPrompt.mode !== mode) {
        return false;
      }
      if (!normalizedKeyword) {
        return true;
      }

      return (
        includesKeyword(localizedPrompt.title, normalizedKeyword) ||
        includesKeyword(localizedPrompt.prompt, normalizedKeyword) ||
        includesKeyword(localizedPrompt.author, normalizedKeyword) ||
        includesKeyword(localizedPrompt.category, normalizedKeyword) ||
        includesKeyword(localizedPrompt.subCategory, normalizedKeyword) ||
        includesKeyword(localizedPrompt.sourceLabel, normalizedKeyword)
      );
    });
  }, [category, keyword, mode, nsfwFilter, promptLanguage, promptPool]);

  const visiblePrompts = filteredPrompts.slice(0, visibleCount);
  const hasMore = visiblePrompts.length < filteredPrompts.length;
  const selectedLanguageLabel = promptLanguage === "zh-CN" ? "" : "English";
  const selectedCategoryLabel = category === ALL_CATEGORY_VALUE ? "" : category;
  const selectedModeLabel = mode === "all" ? "" : mode === "edit" ? "编辑" : "文生图";
  const selectedNsfwLabel =
    nsfwFilter === "safe" ? "" : nsfwFilter === "include" ? "包含 NSFW" : "仅 NSFW";
  const selectedFavoriteLabel = favoriteFilter === "favorites" ? "已收藏" : "";
  const activeFilterLabels = [
    selectedFavoriteLabel,
    selectedLanguageLabel,
    selectedCategoryLabel,
    selectedModeLabel,
    selectedNsfwLabel,
  ].filter(Boolean);
  const activeFilterCount = activeFilterLabels.length;

  const resetFilters = () => {
    setFavoriteFilter("all");
    setPromptLanguage("zh-CN");
    setCategory(ALL_CATEGORY_VALUE);
    setMode("all");
    setNsfwFilter("safe");
  };

  const setFavoriteBusy = (id: string, busy: boolean) => {
    setFavoriteBusyIds((current) => {
      const next = new Set(current);
      if (busy) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  };

  const toggleFavorite = async (prompt: BananaPrompt) => {
    const key = promptFavoriteKey(prompt);
    if (favoriteBusyIds.has(key)) {
      return;
    }

    const existing = favoriteByPromptKey.get(key);
    setFavoriteBusy(key, true);
    try {
      if (existing) {
        const data = await deletePromptFavorite(existing.id);
        updateFavoriteItems(data.items);
        toast.success("已取消收藏");
      } else {
        const data = await createPromptFavorite(prompt);
        updateFavoriteItems(data.items);
        toast.success("已收藏");
      }
    } catch (toggleError) {
      toast.error(toggleError instanceof Error ? toggleError.message : "收藏操作失败");
    } finally {
      setFavoriteBusy(key, false);
    }
  };

  const renderFavoriteTabs = (className?: string) => (
    <div className={cn("flex h-10 rounded-full bg-[#f0f0f0] p-1", className)}>
      <button
        type="button"
        className={cn(
          "inline-flex min-w-0 flex-1 items-center justify-center rounded-full px-3 text-xs font-semibold text-[#45515e] transition",
          favoriteFilter === "all" && "bg-white text-[#18181b] shadow-sm",
        )}
        onClick={() => setFavoriteFilter("all")}
      >
        全部
      </button>
      <button
        type="button"
        className={cn(
          "inline-flex min-w-0 flex-1 items-center justify-center gap-1.5 rounded-full px-3 text-xs font-semibold text-[#45515e] transition",
          favoriteFilter === "favorites" && "bg-white text-[#1456f0] shadow-sm",
        )}
        onClick={() => setFavoriteFilter("favorites")}
      >
        <Star className={cn("size-3.5", favoriteFilter === "favorites" && "fill-current")} />
        {favoriteItems.length > 0 ? `收藏 ${favoriteItems.length}` : "收藏"}
      </button>
    </div>
  );

  const renderFilterControls = (triggerClassName?: string) => (
    <>
      <Select
        value={promptLanguage}
        onValueChange={(value) => setPromptLanguage(value as PromptMarketLanguage)}
      >
        <SelectTrigger className={triggerClassName} aria-label="筛选提示词语言">
          <SelectValue placeholder="语言" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value="zh-CN">中文</SelectItem>
            <SelectItem value="en">English</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      <Select value={category} onValueChange={setCategory}>
        <SelectTrigger className={triggerClassName} aria-label="筛选提示词分类">
          <SelectValue placeholder="分类" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value={ALL_CATEGORY_VALUE}>全部分类</SelectItem>
            {categories.map((item) => (
              <SelectItem key={item} value={item}>
                {item}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
      <Select value={mode} onValueChange={(value) => setMode(value as PromptMarketModeFilter)}>
        <SelectTrigger className={triggerClassName} aria-label="筛选提示词模式">
          <SelectValue placeholder="模式" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value="all">全部模式</SelectItem>
            <SelectItem value="generate">文生图</SelectItem>
            <SelectItem value="edit">编辑</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      {isAdmin ? (
        <Select
          value={nsfwFilter}
          onValueChange={(value) => setNsfwFilter(value as PromptMarketNsfwFilter)}
        >
          <SelectTrigger className={triggerClassName} aria-label="筛选 NSFW 提示词">
            <SelectValue placeholder="NSFW" />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="safe">隐藏 NSFW</SelectItem>
              <SelectItem value="include">包含 NSFW</SelectItem>
              <SelectItem value="only">仅 NSFW</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      ) : null}
    </>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(94dvh,860px)] w-[min(96vw,1180px)] max-w-none flex-col overflow-hidden rounded-[24px] p-0 sm:h-[min(90dvh,860px)] sm:rounded-[28px]">
        <DialogHeader className="border-b border-[#f2f3f5] px-4 pt-4 pr-12 pb-3 sm:px-6 sm:pt-5 sm:pr-14 sm:pb-4">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <DialogTitle className="text-xl leading-tight sm:text-2xl">Prompts 提示词市场</DialogTitle>
            </div>
            <div className="flex shrink-0 items-center gap-2 pt-0.5 text-xs text-[#8e8e93]">
              <span className="rounded-full bg-[#f0f0f0] px-2.5 py-1 sm:px-3">
                {favoriteFilter === "favorites"
                  ? isLoadingFavorites
                    ? "读取收藏"
                    : `已收藏 ${filteredPrompts.length}`
                  : prompts.length > 0
                    ? `${filteredPrompts.length} / ${prompts.length}`
                    : "公开图库"}
              </span>
            </div>
          </div>
        </DialogHeader>

        <div className="border-b border-[#f2f3f5] px-4 py-2 sm:px-6 sm:py-2.5">
          <div className="md:hidden">
            {renderFavoriteTabs()}
            <div className="mt-2 flex items-center gap-2">
              <div className="relative min-w-0 flex-1">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-[#8e8e93]" />
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  placeholder="搜索提示词"
                  className="h-10 pl-9"
                  aria-label="搜索提示词"
                />
              </div>
              <button
                type="button"
                className={cn(
                  "relative inline-flex size-10 shrink-0 items-center justify-center rounded-full border border-[#e5e7eb] bg-white text-[#45515e] transition hover:bg-black/[0.05]",
                  isMobileFiltersOpen && "border-[#bfdbfe] bg-[#eef4ff] text-[#1456f0]",
                )}
                onClick={() => setIsMobileFiltersOpen((open) => !open)}
                aria-label={isMobileFiltersOpen ? "收起筛选项" : "展开筛选项"}
                aria-expanded={isMobileFiltersOpen}
                title={isMobileFiltersOpen ? "收起筛选" : "筛选"}
              >
                <SlidersHorizontal className="size-4" />
                {activeFilterCount > 0 ? (
                  <span className="absolute -right-0.5 -top-0.5 inline-flex size-4 items-center justify-center rounded-full bg-[#1456f0] text-[10px] font-semibold text-white">
                    {activeFilterCount}
                  </span>
                ) : null}
              </button>
            </div>

            {activeFilterLabels.length > 0 && !isMobileFiltersOpen ? (
              <div className="hide-scrollbar mt-2 flex gap-1.5 overflow-x-auto">
                {activeFilterLabels.map((label) => (
                  <span
                    key={label}
                    className="shrink-0 rounded-full bg-[#f0f0f0] px-2.5 py-1 text-[11px] font-medium text-[#45515e]"
                  >
                    {label}
                  </span>
                ))}
                <button
                  type="button"
                  className="shrink-0 rounded-full px-2.5 py-1 text-[11px] font-medium text-[#1456f0]"
                  onClick={resetFilters}
                >
                  清除
                </button>
              </div>
            ) : null}

            {isMobileFiltersOpen ? (
              <div className="mt-2 grid grid-cols-2 gap-2">
                {renderFilterControls()}
                <Button
                  type="button"
                  variant="outline"
                  className="col-span-2 h-9 rounded-full border-[#e5e7eb] bg-white text-xs text-[#45515e] shadow-none"
                  onClick={resetFilters}
                  disabled={activeFilterCount === 0}
                >
                  重置筛选
                </Button>
              </div>
            ) : null}
          </div>

          <div className="hidden md:flex md:flex-col md:gap-1.5">
            <div className="flex min-w-0 gap-2">
              <div className="relative min-w-0 flex-1">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-[#8e8e93]" />
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  placeholder="搜索标题、作者、分类或提示词"
                  className="pl-9"
                  aria-label="搜索标题、作者、分类或提示词"
                />
              </div>
              {renderFavoriteTabs("w-[168px] shrink-0")}
            </div>
            <div className={cn("grid gap-2", isAdmin ? "md:grid-cols-[minmax(180px,1fr)_120px_minmax(160px,1fr)_140px]" : "md:grid-cols-[minmax(180px,1fr)_120px_minmax(160px,1fr)]")}>
              {renderFilterControls("min-w-0")}
            </div>
          </div>
          {favoriteError ? (
            <div className="mt-2 flex items-center justify-between gap-3 rounded-[12px] bg-[#fff7ed] px-3 py-2 text-xs text-[#9a3412]">
              <span>{favoriteError}</span>
              <button type="button" className="font-semibold text-[#1456f0]" onClick={loadFavoriteData}>
                重试
              </button>
            </div>
          ) : null}
        </div>

        <div ref={scrollAreaRef} className="min-h-0 flex-1 overflow-y-auto bg-white px-4 py-2.5 sm:px-6 sm:py-3">
          {favoriteFilter !== "favorites" && isLoading ? (
            <div className="flex h-full min-h-[320px] flex-col items-center justify-center gap-3 text-[#45515e]">
              <LoaderCircle className="size-6 animate-spin text-[#1456f0]" />
              <p className="text-sm">正在读取公开图库...</p>
            </div>
          ) : favoriteFilter !== "favorites" && error ? (
            <div className="flex h-full min-h-[320px] flex-col items-center justify-center gap-4 text-center">
              <div className="max-w-[420px] text-sm leading-6 text-[#45515e]">{error}</div>
              <Button
                type="button"
                variant="outline"
                className="rounded-full"
                onClick={loadPromptData}
              >
                <RefreshCcw className="size-4" />
                重新加载
              </Button>
            </div>
          ) : favoriteFilter === "favorites" && isLoadingFavorites && favoriteItems.length === 0 ? (
            <div className="flex h-full min-h-[320px] flex-col items-center justify-center gap-3 text-[#45515e]">
              <LoaderCircle className="size-6 animate-spin text-[#1456f0]" />
              <p className="text-sm">正在读取收藏...</p>
            </div>
          ) : visiblePrompts.length === 0 ? (
            <div className="flex h-full min-h-[320px] items-center justify-center text-sm text-[#8e8e93]">
              {favoriteFilter === "favorites"
                ? favoriteItems.length === 0
                  ? "还没有收藏提示词"
                  : "没有匹配的收藏提示词"
                : "没有找到匹配的提示词"}
            </div>
          ) : (
            <div className="flex flex-col gap-3.5">
              <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                {visiblePrompts.map((prompt) => {
                  const localizedPrompt = getLocalizedPrompt(prompt, promptLanguage);
                  const dateLabel = formatPromptDate(prompt.created);
                  const promptMetaLabels = [localizedPrompt.subCategory, dateLabel].filter(
                    (label): label is string => Boolean(label),
                  );
                  const favoriteKey = promptFavoriteKey(prompt);
                  const isFavorite = favoriteIds.has(favoriteKey);
                  const isFavoriteBusy = favoriteBusyIds.has(favoriteKey);
                  return (
                    <article
                      key={prompt.id}
                    className="group overflow-hidden rounded-[20px] border border-[#f2f3f5] bg-white shadow-[0_4px_6px_rgba(0,0,0,0.08)] transition hover:-translate-y-0.5 hover:shadow-[0_12px_16px_-4px_rgba(36,36,36,0.08)]"
                    >
                      <div className="relative aspect-[16/10] overflow-hidden bg-[#f0f0f0]">
                        <PromptPreviewImage prompt={localizedPrompt} />
                        {localizedPrompt.author ? (
                          <div className="absolute left-3 top-3 z-10 flex max-w-[calc(100%-1.5rem)]">
                            <span
                              className="min-w-0 truncate rounded-full bg-black/45 px-2 py-1 text-[11px] font-medium text-white shadow-sm backdrop-blur-sm"
                              title={`作者：${localizedPrompt.author}`}
                            >
                              {localizedPrompt.author}
                            </span>
                          </div>
                        ) : null}
                        <div className="absolute inset-x-0 bottom-0 flex flex-wrap items-center gap-1 bg-gradient-to-t from-black/70 via-black/25 to-transparent px-2.5 pt-7 pb-2">
                          <Badge className="bg-white/92 px-1.5 py-0.5 text-[10px] text-[#18181b] shadow-sm">
                            {localizedPrompt.mode === "edit" ? "编辑" : "文生图"}
                          </Badge>
                          <Badge className="bg-white/18 px-1.5 py-0.5 text-[10px] text-white shadow-sm backdrop-blur">
                            {localizedPrompt.category}
                          </Badge>
                          {prompt.isNsfw ? (
                            <Badge className="bg-white/18 px-1.5 py-0.5 text-[10px] text-white shadow-sm backdrop-blur">
                              NSFW
                            </Badge>
                          ) : null}
                          {prompt.referenceImageUrls.length > 0 ? (
                            <Badge className="bg-white/18 px-1.5 py-0.5 text-[10px] text-white shadow-sm backdrop-blur">
                              {prompt.referenceImageUrls.length} 张参考图
                            </Badge>
                          ) : null}
                        </div>
                      </div>
                      <div className="flex min-h-[182px] flex-col gap-2.5 p-3.5">
                        <div className="flex min-w-0 items-start justify-between gap-3">
                          <div className="min-w-0">
                            <h3 className="font-display truncate text-[15px] font-semibold text-[#222222]">
                              {localizedPrompt.title}
                            </h3>
                            {promptMetaLabels.length > 0 ? (
                              <div className="mt-0.5 flex flex-wrap items-center gap-1 text-[10px] text-[#8e8e93]">
                                {promptMetaLabels.map((label) => (
                                  <span key={label}>/{label}</span>
                                ))}
                              </div>
                            ) : null}
                          </div>
                          <div className="flex shrink-0 items-center gap-1.5">
                            <button
                              type="button"
                              className={cn(
                                "inline-flex size-8 items-center justify-center rounded-full border border-[#e5e7eb] text-[#45515e] transition hover:bg-black/[0.05] hover:text-[#18181b] disabled:cursor-not-allowed disabled:opacity-60",
                                isFavorite && "border-[#bfdbfe] bg-[#eef4ff] text-[#1456f0]",
                              )}
                              onClick={() => void toggleFavorite(prompt)}
                              disabled={isFavoriteBusy}
                              aria-label={isFavorite ? "取消收藏提示词" : "收藏提示词"}
                              title={isFavorite ? "取消收藏" : "收藏"}
                            >
                              {isFavoriteBusy ? (
                                <LoaderCircle className="size-3.5 animate-spin" />
                              ) : (
                                <Star className={cn("size-3.5", isFavorite && "fill-current")} />
                              )}
                            </button>
                            {prompt.link ? (
                              <a
                                href={prompt.link}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex size-8 items-center justify-center rounded-full border border-[#e5e7eb] text-[#45515e] transition hover:bg-black/[0.05] hover:text-[#18181b]"
                                aria-label="查看来源"
                                title="查看来源"
                              >
                                <ExternalLink className="size-3.5" />
                              </a>
                            ) : null}
                          </div>
                        </div>
                        <p className="line-clamp-3 text-[13px] leading-5 text-[#45515e]">{localizedPrompt.prompt}</p>
                        <div className="mt-auto flex justify-end border-t border-[#f2f3f5] pt-2.5">
                          <Button
                            type="button"
                            size="sm"
                            className="h-7 rounded-full bg-[#1456f0] px-3.5 text-[11px] text-white shadow-sm hover:bg-[#2563eb]"
                            onClick={() => void onApplyPrompt(localizedPrompt)}
                          >
                            套用
                          </Button>
                        </div>
                      </div>
                    </article>
                  );
                })}
              </div>
              {hasMore ? (
                <div className="flex justify-center pt-1">
                  <Button
                    type="button"
                    variant="outline"
                    className="rounded-full"
                    onClick={() => setVisibleCount((current) => current + VISIBLE_COUNT_STEP)}
                  >
                    加载更多 ({visiblePrompts.length}/{filteredPrompts.length})
                  </Button>
                </div>
              ) : null}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
