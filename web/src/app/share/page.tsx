"use client";

import { useEffect, useMemo, useState } from "react";
import { ArrowRight, Copy, Download, ImageDown, Info, LoaderCircle, Share2, Sparkles, WandSparkles } from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { fetchImageShare, type ImageShare } from "@/lib/api";
import { isWeChatBrowser, rememberPendingInviteCode, shareSafely } from "@/lib/share-helpers";

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

function shareText(url: string) {
  return `我在 1818 生成了一张图片，你可以打开链接生成同款:\n${url}\n\n1818 AI 商业图片创作台，上传参考图、输入提示词，快速生成商业视觉素材。`;
}

const heroSlogan = "1818 AI 商业图片创作台";
const heroGift = "上传参考图，一键生成同款商业素材";

function metaValue(value: unknown, fallback: string) {
  const text = typeof value === "string" ? value.trim() : "";
  return text || fallback;
}

function imageAspectClass(size: string | undefined) {
  const [width, height] = String(size || "").split(/[x×]/i).map((part) => Number(part.trim()));
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return "aspect-[4/5]";
  }
  if (width > height * 1.2) {
    return "aspect-video";
  }
  if (height > width * 1.2) {
    return "aspect-[4/5]";
  }
  return "aspect-square";
}

function roundedRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, width: number, height: number, radius: number) {
  const r = Math.min(radius, width / 2, height / 2);
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + width, y, x + width, y + height, r);
  ctx.arcTo(x + width, y + height, x, y + height, r);
  ctx.arcTo(x, y + height, x, y, r);
  ctx.arcTo(x, y, x + width, y, r);
  ctx.closePath();
}

function drawWrappedText(
  ctx: CanvasRenderingContext2D,
  text: string,
  x: number,
  y: number,
  maxWidth: number,
  lineHeight: number,
  maxLines: number,
) {
  const chars = Array.from(text);
  const lines: string[] = [];
  let line = "";
  for (const char of chars) {
    const nextLine = line + char;
    if (ctx.measureText(nextLine).width > maxWidth && line) {
      lines.push(line);
      line = char;
      if (lines.length >= maxLines) {
        break;
      }
      continue;
    }
    line = nextLine;
  }
  if (line && lines.length < maxLines) {
    lines.push(line);
  }
  lines.forEach((item, index) => {
    const output = index === maxLines - 1 && lines.length === maxLines && chars.join("").length > lines.join("").length
      ? item.slice(0, Math.max(0, item.length - 1)) + "..."
      : item;
    ctx.fillText(output, x, y + index * lineHeight);
  });
}

function loadImage(src: string) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    image.crossOrigin = "anonymous";
    image.onload = () => resolve(image);
    image.onerror = reject;
    image.src = src;
  });
}

function downloadCanvas(canvas: HTMLCanvasElement, filename: string) {
  const link = document.createElement("a");
  link.download = filename;
  link.href = canvas.toDataURL("image/png");
  link.click();
}

async function imageFileFromURL(url: string) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error("图片加载失败");
  }
  const blob = await response.blob();
  const extension = blob.type.includes("jpeg") ? "jpg" : blob.type.includes("webp") ? "webp" : "png";
  return new File([blob], `1818-image-share.${extension}`, { type: blob.type || "image/png" });
}

// nativeShare delegates to shareSafely so we never trigger navigator.share inside the WeChat
// in-app browser (where it crashes the WebView). Instead we surface a toast hint and copy
// the link, letting the user use the built-in 3-dot menu to share.
async function nativeShare(share: ImageShare, prompt: string) {
  const url = share.share_url || window.location.href;
  const title = "1818 作品分享";
  const text = prompt
    ? `我在 1818 生成了一张图片，你可以打开链接生成同款：${prompt.slice(0, 80)}`
    : "我在 1818 生成了一张图片，你可以打开链接生成同款。";
  let files: File[] | undefined;
  try {
    files = [await imageFileFromURL(share.image_url)];
  } catch {
    files = undefined;
  }
  const result = await shareSafely({ url, title, text, files });
  if (result.mode === "native" || result.mode === "aborted") {
    return;
  }
  if (result.mode === "wechat-hint") {
    await copyText(shareText(url));
    toast.message("请点击右上角 ··· 选择「转发」或「分享到朋友圈」", { description: "分享文案已复制。微信内部不支持系统分享，请使用顶部菜单。" });
    return;
  }
  await copyText(shareText(url));
  toast.success("分享文案已复制");
}

async function createShareCardImage(share: ImageShare, prompt: string) {
  const canvas = document.createElement("canvas");
  canvas.width = 1080;
  canvas.height = 1560;
  const ctx = canvas.getContext("2d");
  if (!ctx) {
    throw new Error("浏览器不支持生成分享卡片");
  }

  const bg = ctx.createLinearGradient(0, 0, 1080, 1560);
  bg.addColorStop(0, "#f8fbff");
  bg.addColorStop(0.48, "#ffffff");
  bg.addColorStop(1, "#fff4fb");
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, 1080, 1560);

  ctx.strokeStyle = "rgba(20, 86, 240, 0.08)";
  ctx.lineWidth = 1;
  for (let x = 0; x <= 1080; x += 72) {
    ctx.beginPath();
    ctx.moveTo(x, 0);
    ctx.lineTo(x, 1560);
    ctx.stroke();
  }
  for (let y = 0; y <= 1560; y += 72) {
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(1080, y);
    ctx.stroke();
  }

  ctx.save();
  roundedRectPath(ctx, 62, 62, 956, 1436, 52);
  ctx.fillStyle = "rgba(255, 255, 255, 0.96)";
  ctx.fill();
  ctx.shadowColor = "rgba(44, 30, 116, 0.16)";
  ctx.shadowBlur = 34;
  ctx.strokeStyle = "rgba(20, 86, 240, 0.18)";
  ctx.lineWidth = 2;
  ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.restore();

  const image = await loadImage(share.image_url);
  const imageBox = { x: 102, y: 104, width: 876, height: 760 };
  ctx.save();
  roundedRectPath(ctx, imageBox.x, imageBox.y, imageBox.width, imageBox.height, 34);
  ctx.clip();
  ctx.fillStyle = "#020617";
  ctx.fillRect(imageBox.x, imageBox.y, imageBox.width, imageBox.height);
  const scale = Math.min(imageBox.width / image.width, imageBox.height / image.height);
  const width = image.width * scale;
  const height = image.height * scale;
  ctx.drawImage(image, imageBox.x + (imageBox.width - width) / 2, imageBox.y + (imageBox.height - height) / 2, width, height);
  ctx.restore();

  ctx.fillStyle = "#18181b";
  ctx.font = "700 56px sans-serif";
  ctx.fillText("1818", 102, 950);
  ctx.fillStyle = "#1456f0";
  ctx.font = "700 30px sans-serif";
  ctx.fillText(heroSlogan, 104, 1000);
  ctx.fillStyle = "#45515e";
  ctx.font = "500 25px sans-serif";
  ctx.fillText(heroGift, 104, 1040);

  ctx.fillStyle = "#181e25";
  roundedRectPath(ctx, 752, 946, 226, 70, 35);
  ctx.fill();
  ctx.fillStyle = "#ffffff";
  ctx.font = "700 24px sans-serif";
  ctx.fillText("生成同款", 810, 990);

  ctx.fillStyle = "#334155";
  ctx.font = "500 27px sans-serif";
  drawWrappedText(ctx, prompt || "该分享没有附带提示词。", 102, 1124, 876, 43, 4);

  const metas = [
    ["模型", metaValue(share.model, "Image-2")],
    ["尺寸", metaValue(share.size, "Auto")],
    ["画质", metaValue(share.quality, "High")],
    ["结果", `第 ${share.result_index || 1} 张`],
  ];
  metas.forEach(([label, value], index) => {
    const x = 102 + (index % 2) * 448;
    const y = 1360 + Math.floor(index / 2) * 72;
    ctx.fillStyle = "#edf4ff";
    roundedRectPath(ctx, x, y, 408, 52, 26);
    ctx.fill();
    ctx.fillStyle = "#1456f0";
    ctx.font = "500 20px sans-serif";
    ctx.fillText(label, x + 24, y + 34);
    ctx.fillStyle = "#18181b";
    ctx.font = "700 22px sans-serif";
    ctx.fillText(value, x + 116, y + 34);
  });

  return canvas;
}

export default function SharePage() {
  const location = useLocation();
  const shareId = useMemo(() => new URLSearchParams(location.search).get("id") || "", [location.search]);
  const [share, setShare] = useState<ImageShare | null>(null);
  const [isLoading, setIsLoading] = useState(Boolean(shareId));
  const [isSharing, setIsSharing] = useState(false);
  const [isCardDownloading, setIsCardDownloading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const root = document.documentElement;
    const wasDark = root.classList.contains("dark");
    root.classList.add("share-page-root");
    document.body.classList.add("share-page-root");
    // The share page is read-only marketing surface for unauthenticated visitors and is
    // designed exclusively in the light palette. We temporarily disable the dark variant
    // so the meta cards (white-on-light) stay readable, restoring the user preference on
    // unmount so navigating elsewhere keeps the chosen theme.
    if (wasDark) {
      root.classList.remove("dark");
    }
    return () => {
      root.classList.remove("share-page-root");
      document.body.classList.remove("share-page-root");
      if (wasDark) {
        root.classList.add("dark");
      }
    };
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const ref = params.get("ref") || params.get("invite");
    if (ref) {
      rememberPendingInviteCode(ref, "share-page-url");
    }
  }, [location.search]);

  useEffect(() => {
    if (!shareId) {
      setError("分享链接缺少图片 ID");
      setIsLoading(false);
      return;
    }
    let active = true;
    const load = async () => {
      try {
        const data = await fetchImageShare(shareId);
        if (active) {
          setShare(data);
          setError("");
          if (data.inviter_invite_code) {
            rememberPendingInviteCode(data.inviter_invite_code, "share-page-api");
          }
        }
      } catch (requestError) {
        if (active) {
          setError(requestError instanceof Error ? requestError.message : "分享图片不存在或已失效");
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [shareId]);

  const prompt = (share?.prompt || share?.revised_prompt || "").trim();
  const wechatBrowser = useMemo(() => isWeChatBrowser(), []);
  const sameURL = prompt
    ? `/image?prompt=${encodeURIComponent(prompt)}&from_share=${encodeURIComponent(share?.id || shareId)}`
    : "/image";

  const handleDownloadCard = async () => {
    if (!share) {
      return;
    }
    setIsCardDownloading(true);
    try {
      const canvas = await createShareCardImage(share, prompt);
      downloadCanvas(canvas, `1818-image-share-${share.id}.png`);
      toast.success("分享卡片已生成");
    } catch (downloadError) {
      toast.error(downloadError instanceof Error ? downloadError.message : "分享卡片生成失败");
    } finally {
      setIsCardDownloading(false);
    }
  };

  const handleNativeShare = async () => {
    if (!share) {
      return;
    }
    setIsSharing(true);
    try {
      await nativeShare(share, prompt);
    } catch (shareError) {
      if (shareError instanceof DOMException && shareError.name === "AbortError") {
        return;
      }
      toast.error(shareError instanceof Error ? shareError.message : "分享失败");
    } finally {
      setIsSharing(false);
    }
  };

  return (
    <main
      className="min-h-screen overflow-y-auto px-4 py-5 pb-28 text-[#172033] sm:px-6 sm:pb-5 lg:px-8"
      style={{
        minHeight: "100vh",
        backgroundColor: "#f6f8fc",
        background:
          "radial-gradient(circle at 15% 8%, rgba(64, 145, 255, 0.20), transparent 28%), radial-gradient(circle at 88% 14%, rgba(255, 190, 116, 0.22), transparent 30%), linear-gradient(180deg, #f8fbff 0%, #f6f8fc 48%, #fffaf3 100%)",
      }}
    >
      <div className="mx-auto flex min-h-[calc(100vh-3rem)] w-full max-w-6xl items-center justify-center">
        <section className="w-full rounded-[34px] border border-[#e4ebf5] bg-white p-3 shadow-[0_28px_90px_rgba(36,48,71,0.14)] sm:p-5">
          {isLoading ? (
            <div className="flex min-h-[520px] items-center justify-center rounded-[28px] bg-white">
              <LoaderCircle className="size-7 animate-spin text-[#1456f0]" />
            </div>
          ) : error ? (
            <div className="flex min-h-[520px] items-center justify-center rounded-[28px] bg-white">
              <div className="rounded-3xl border border-rose-200 bg-rose-50 px-6 py-5 text-center text-rose-700">
                {error}
              </div>
            </div>
          ) : (
            <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_400px]">
              <article className="overflow-hidden rounded-[30px] border border-[#dfe7f2] bg-[#fffdfa] shadow-[0_14px_38px_rgba(31,45,61,0.10)]">
                <div className="border-b border-[#edf1f7] bg-[#fbfdff] px-5 py-4 sm:px-6">
                  <div className="flex items-center justify-between gap-4">
                    <div className="flex items-center gap-3">
                      <img
                        src="/logo-1818.svg"
                        alt="1818"
                        width={140}
                        height={40}
                        decoding="async"
                        className="h-10 w-[140px] bg-white"
                      />
                      <div>
                        <div className="text-xl font-bold text-[#18181b]">1818</div>
                        <div className="text-xs font-semibold uppercase tracking-[0.24em] text-[#1456f0]">Share Card</div>
                      </div>
                    </div>
                    <div className="hidden rounded-full border border-[#bfdbfe] bg-[#edf4ff] px-3 py-1 text-xs font-semibold text-[#1456f0] sm:block">
                      {heroGift}
                    </div>
                  </div>
                  <div className="mt-4 rounded-[24px] border border-[#bfdbfe] bg-[linear-gradient(135deg,#eff6ff_0%,#ffffff_54%,#fff7ed_100%)] px-5 py-4 shadow-[0_14px_28px_rgba(20,86,240,0.10)]">
                    <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.24em] text-[#1456f0]">
                      <WandSparkles className="size-4" />
                      AI Image
                    </div>
                    <div className="mt-2 text-2xl font-black tracking-[-0.04em] text-[#111827] sm:text-3xl">
                      {heroSlogan}
                    </div>
                    <div className="mt-1 text-sm font-semibold text-[#45515e]">
                      {heroGift}
                    </div>
                  </div>
                </div>
                <div className="p-4 sm:p-6">
                  <div className={`flex min-h-[280px] items-center justify-center overflow-hidden rounded-[24px] border border-[#dfe7f2] bg-[#f8fafc] ${imageAspectClass(share?.size)}`}>
                    <img
                      src={share?.image_url}
                      alt="Shared generated image"
                      loading="eager"
                      decoding="async"
                      fetchPriority="high"
                      className="h-full max-h-[72svh] w-full object-contain"
                    />
                  </div>
                  <div className="mt-5 rounded-[24px] border border-[#dbeafe] bg-[#f8fbff] p-5">
                    <div className="mb-3 flex items-center gap-2 text-sm font-bold text-[#1456f0]">
                      <Sparkles className="size-4" />
                      生成提示词
                    </div>
                    <p className="max-h-64 overflow-y-auto whitespace-pre-wrap text-sm leading-7 text-[#334155]">
                      {prompt || "该分享没有附带提示词。"}
                    </p>
                  </div>
                </div>
              </article>

              <aside className="flex flex-col gap-4 rounded-[30px] border border-[#dfe7f2] bg-[#ffffff] p-5 shadow-[0_14px_38px_rgba(31,45,61,0.10)]">
                <div>
                  <div className="text-sm font-semibold uppercase tracking-[0.22em] text-[#1456f0]">1818 Share</div>
                  <h1 className="mt-2 text-3xl font-black text-[#111827]">一键分享生成图</h1>
                  <p className="mt-3 text-sm leading-6 text-[#45515e]">
                    点击下方按钮会优先打开系统分享面板，并携带生成图作为分享缩略图。
                  </p>
                </div>

                <div className="grid grid-cols-2 gap-3 text-sm">
                  <div className="rounded-2xl border border-[#e5e7eb] bg-[#f8fafc] p-4">
                    <div className="text-xs text-[#1456f0]">模型</div>
                    <div className="mt-2 truncate font-bold text-[#18181b]">{metaValue(share?.model, "Image-2")}</div>
                  </div>
                  <div className="rounded-2xl border border-[#e5e7eb] bg-[#f8fafc] p-4">
                    <div className="text-xs text-[#1456f0]">尺寸</div>
                    <div className="mt-2 truncate font-bold text-[#18181b]">{metaValue(share?.size, "Auto")}</div>
                  </div>
                  <div className="rounded-2xl border border-[#e5e7eb] bg-[#f8fafc] p-4">
                    <div className="text-xs text-[#1456f0]">画质</div>
                    <div className="mt-2 truncate font-bold text-[#18181b]">{metaValue(share?.quality, "High")}</div>
                  </div>
                  <div className="rounded-2xl border border-[#e5e7eb] bg-[#f8fafc] p-4">
                    <div className="text-xs text-[#1456f0]">结果</div>
                    <div className="mt-2 truncate font-bold text-[#18181b]">第 {share?.result_index || 1} 张</div>
                  </div>
                </div>

                <div className="mt-auto flex flex-col gap-3">
                  {wechatBrowser ? (
                    <div className="flex items-start gap-2 rounded-2xl border border-[#fde68a] bg-[#fffbeb] p-3 text-xs leading-5 text-[#92400e]">
                      <Info className="mt-0.5 size-4 shrink-0" />
                      <span>
                        微信内置浏览器不支持系统分享，请点击右上角 <strong>···</strong> 选择「转发」或「分享到朋友圈」。下方按钮会自动复制分享文案。
                      </span>
                    </div>
                  ) : null}
                  <Button
                    className="h-12 rounded-2xl bg-[#1456f0] text-white shadow-[0_14px_28px_rgba(20,86,240,0.22)] hover:bg-[#0f46c8]"
                    onClick={() => void handleNativeShare()}
                    disabled={isSharing}
                  >
                    {isSharing ? <LoaderCircle className="size-4 animate-spin" /> : <Share2 className="size-4" />}
                    {wechatBrowser ? "复制分享文案" : "立即分享给好友"}
                  </Button>
                  <Button asChild className="h-12 rounded-2xl bg-[#181e25] text-white hover:bg-[#10151c]">
                    <Link to={sameURL}>
                      生成同款
                      <ArrowRight className="size-4" />
                    </Link>
                  </Button>
                  <Button
                    className="h-12 rounded-2xl border border-[#dbeafe] bg-white text-[#18181b] hover:bg-[#edf4ff]"
                    onClick={handleDownloadCard}
                    disabled={isCardDownloading}
                  >
                    {isCardDownloading ? <LoaderCircle className="size-4 animate-spin" /> : <ImageDown className="size-4" />}
                    下载分享卡片
                  </Button>
                  <Button asChild className="h-12 rounded-2xl border border-[#dbeafe] bg-white text-[#18181b] hover:bg-[#edf4ff]">
                    <a href={share?.image_url} download target="_blank" rel="noopener noreferrer">
                      <Download className="size-4" />
                      下载原图
                    </a>
                  </Button>
                  <Button
                    className="h-12 rounded-2xl border border-[#dbeafe] bg-white text-[#18181b] hover:bg-[#edf4ff]"
                    onClick={() => {
                      const url = share?.share_url || window.location.href;
                      void copyText(shareText(url)).then(() => toast.success("分享文案已复制"));
                    }}
                  >
                    <Copy className="size-4" />
                    复制分享文案
                  </Button>
                </div>
              </aside>
            </div>
          )}
        </section>
      </div>
      {share && !isLoading && !error ? (
        <div className="fixed inset-x-0 bottom-0 z-50 border-t border-[#dfe7f2] bg-white/94 px-4 pb-[calc(env(safe-area-inset-bottom)+12px)] pt-3 shadow-[0_-18px_40px_rgba(31,45,61,0.14)] backdrop-blur sm:hidden">
          <div className="mx-auto grid max-w-md grid-cols-[1fr_auto] gap-2">
            <Button
              className="h-12 rounded-2xl bg-[#1456f0] text-white shadow-[0_12px_24px_rgba(20,86,240,0.22)] hover:bg-[#0f46c8]"
              onClick={() => void handleNativeShare()}
              disabled={isSharing}
            >
              {isSharing ? <LoaderCircle className="size-4 animate-spin" /> : <Share2 className="size-4" />}
              分享好友
            </Button>
            <Button asChild className="h-12 rounded-2xl bg-[#181e25] px-4 text-white hover:bg-[#10151c]">
              <Link to={sameURL}>生成同款</Link>
            </Button>
          </div>
        </div>
      ) : null}
    </main>
  );
}
