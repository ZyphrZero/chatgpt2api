"use client";

import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import {
  ArrowRight,
  Check,
  Crown,
  LoaderCircle,
  RefreshCw,
  Sparkles,
} from "lucide-react";
import { motion } from "motion/react";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { createSubscriptionCheckout, fetchSubscriptionConfig, fetchSubscriptionOrder, verifySession, type SubscriptionConfig, type SubscriptionOrder } from "@/lib/api";
import { authSessionFromLoginResponse, getCachedAuthSession, setVerifiedAuthSession } from "@/lib/session";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { cn } from "@/lib/utils";

type Plan = {
  name: string;
  tag: string;
  price: string;
  unit: string;
  description: string;
  quota: string;
  originalQuota: string;
  accent: "slate" | "blue" | "amber";
  featured?: boolean;
  features: string[];
};

type SubscriptionCheckout = Awaited<ReturnType<typeof createSubscriptionCheckout>>;

function markOrderPaid(
  order: SubscriptionOrder,
  handlers: {
    setPaidOrder: Dispatch<SetStateAction<SubscriptionOrder | null>>;
    setPendingOrder: Dispatch<SetStateAction<SubscriptionOrder | null>>;
    setActiveCheckout: Dispatch<SetStateAction<SubscriptionCheckout | null>>;
    setCheckoutPollStartedAt: Dispatch<SetStateAction<number>>;
    setCheckoutPollTimedOut: Dispatch<SetStateAction<boolean>>;
  },
) {
  handlers.setPaidOrder(order);
  handlers.setPendingOrder(null);
  handlers.setActiveCheckout(null);
  handlers.setCheckoutPollStartedAt(0);
  handlers.setCheckoutPollTimedOut(false);
  void refreshSubscriptionSessionQuota();
  toast.success(`订阅已支付，已增加 ${formatPointAmount(order.quota)} 点`);
}

const fallbackPlans: Plan[] = [
  {
    name: "轻量版",
    tag: "个人起步",
    price: "¥19",
    unit: "/ 月",
    description: "适合低频商品图、头像图、朋友圈素材和日常试用。",
    quota: "300 点",
    originalQuota: "原 100 点",
    accent: "slate",
    features: ["标准生图队列", "PNG / JPEG / WEBP 输出", "个人图片库私密存储", "邀请奖励点数叠加"],
  },
  {
    name: "创作版",
    tag: "推荐",
    price: "¥69",
    unit: "/ 月",
    description: "面向稳定创作，覆盖参考图、高清图、批量任务和更快排队。",
    quota: "1,500 点",
    originalQuota: "原 500 点",
    accent: "blue",
    featured: true,
    features: ["优先创作队列", "参考图与高分辨率任务", "批量生成与重试队列", "图片库分享与公开展示"],
  },
  {
    name: "商业版",
    tag: "团队 / 商用",
    price: "¥199",
    unit: "/ 月",
    description: "适合多用户、店铺素材团队、电商主图和 API 调用场景。",
    quota: "6,000 点",
    originalQuota: "原 2,000 点",
    accent: "amber",
    features: ["商业队列与更高并发", "团队账号与权限管理", "API Token 与调用统计", "专属配置与人工协助"],
  },
];

const CHECKOUT_POLL_INTERVAL_MS = 2_500;
const CHECKOUT_POLL_TIMEOUT_MS = 5 * 60_000;
const QUOTA_REFRESH_EVENT = "chatgpt2api:quota-refresh";

function formatPointAmount(value: number) {
  return new Intl.NumberFormat("zh-CN").format(Math.max(0, Number(value) || 0));
}

function planFromRemote(plan: SubscriptionConfig["plans"][number], index: number): Plan {
  const accent: Plan["accent"] = plan.recommended ? "blue" : index % 3 === 2 ? "amber" : "slate";
  const originalQuotaByIndex = ["原 100 点", "原 500 点", "原 2,000 点"];
  return {
    name: plan.name,
    tag: plan.recommended ? "推荐" : index === 0 ? "个人起步" : "可选套餐",
    price: `¥${plan.price}`,
    unit: "/ 次",
    description: plan.description || "购买后自动增加图片点数，可用于文生图、参考图和高清任务。",
    quota: `${formatPointAmount(plan.quota)} 点`,
    originalQuota: originalQuotaByIndex[index] || `原 ${formatPointAmount(Math.max(100, Math.floor(plan.quota / 3)))} 点`,
    accent,
    featured: Boolean(plan.recommended),
    features: ["支付成功自动到账", "失败任务不扣成功点数", "图片库私密存储", "支持签到和邀请奖励叠加"],
  };
}

function payTypeLabel(payType: string) {
  switch (payType) {
    case "alipay":
      return "支付宝";
    case "wxpay":
      return "微信";
    case "qqpay":
      return "QQ";
    default:
      return payType || "支付";
  }
}

function planAccentClass(plan: Plan) {
  if (plan.accent === "blue") {
    return {
      shell: "border-[#1456f0]/40 bg-[linear-gradient(180deg,#eef6ff_0%,#ffffff_62%,#f7fbff_100%)] shadow-[0_24px_64px_rgba(20,86,240,0.16)] lg:-translate-y-2",
      badge: "bg-[#1456f0] text-white",
      button: "bg-[#1456f0] text-white shadow-[0_10px_28px_rgba(20,86,240,0.26)] hover:bg-[#0d45c9]",
      glow: "from-[#1456f0]/18 via-sky-300/14 to-transparent",
      quota: "bg-[#1456f0] text-white shadow-[0_12px_30px_rgba(20,86,240,0.24)]",
    };
  }
  if (plan.accent === "amber") {
    return {
      shell: "border-amber-300/65 bg-[linear-gradient(180deg,#fff6df_0%,#ffffff_62%,#fff9ed_100%)] shadow-[0_22px_58px_rgba(245,158,11,0.13)]",
      badge: "bg-amber-500 text-white",
      button: "bg-slate-950 text-white hover:bg-amber-600",
      glow: "from-amber-300/24 via-orange-200/16 to-transparent",
      quota: "bg-amber-500 text-white shadow-[0_10px_28px_rgba(245,158,11,0.22)]",
    };
  }
  return {
    shell: "border-slate-200/90 bg-[linear-gradient(180deg,#ffffff_0%,#ffffff_62%,#f8fafc_100%)] shadow-[0_18px_48px_rgba(15,23,42,0.08)]",
    badge: "bg-slate-900 text-white",
    button: "bg-slate-950 text-white hover:bg-slate-700",
    glow: "from-slate-200/34 via-white/18 to-transparent",
    quota: "bg-slate-950 text-white shadow-[0_10px_28px_rgba(15,23,42,0.16)]",
  };
}

async function refreshSubscriptionSessionQuota() {
  const currentSession = getCachedAuthSession();
  if (!currentSession?.key) {
    return;
  }
  try {
    const data = await verifySession(currentSession.key);
    const nextSession = authSessionFromLoginResponse(data, currentSession.key);
    await setVerifiedAuthSession(nextSession);
    window.dispatchEvent(new Event(QUOTA_REFRESH_EVENT));
  } catch {
    // Payment success should stay visible even if the quota refresh races the async notify.
  }
}

export default function SubscriptionPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/subscription");
  const [searchParams] = useSearchParams();
  const [remoteConfig, setRemoteConfig] = useState<SubscriptionConfig | null>(null);
  const [paidOrder, setPaidOrder] = useState<SubscriptionOrder | null>(null);
  const [pendingOrder, setPendingOrder] = useState<SubscriptionOrder | null>(null);
  const [activeCheckout, setActiveCheckout] = useState<SubscriptionCheckout | null>(null);
  const [checkoutPollStartedAt, setCheckoutPollStartedAt] = useState(0);
  const [checkoutPollTimedOut, setCheckoutPollTimedOut] = useState(false);
  const [isLoadingConfig, setIsLoadingConfig] = useState(true);
  const [checkoutPlanId, setCheckoutPlanId] = useState("");

  useEffect(() => {
    if (!session) {
      return;
    }
    let active = true;
    const load = async () => {
      setIsLoadingConfig(true);
      try {
        const data = await fetchSubscriptionConfig();
        if (active) {
          setRemoteConfig(data.config);
        }
      } catch (error) {
        if (active) {
          toast.error(error instanceof Error ? error.message : "读取订阅配置失败");
        }
      } finally {
        if (active) {
          setIsLoadingConfig(false);
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [session]);

  useEffect(() => {
    if (!session) {
      return;
    }
    const orderId = searchParams.get("order") || "";
    if (!orderId) {
      return;
    }
    let active = true;
    const loadOrder = async () => {
      try {
        const data = await fetchSubscriptionOrder(orderId);
        if (!active) {
          return;
        }
        if (data.order.status === "paid") {
          markOrderPaid(data.order, {
            setPaidOrder,
            setPendingOrder,
            setActiveCheckout,
            setCheckoutPollStartedAt,
            setCheckoutPollTimedOut,
          });
        } else {
          setPendingOrder(data.order);
        }
      } catch {
        // Payment providers may redirect before async notify arrives; keep the page usable.
      }
    };
    void loadOrder();
    return () => {
      active = false;
    };
  }, [searchParams, session]);

  useEffect(() => {
    if (!activeCheckout?.order?.id) {
      return;
    }
    let stopped = false;
    const poll = async () => {
      if (checkoutPollStartedAt > 0 && Date.now() - checkoutPollStartedAt > CHECKOUT_POLL_TIMEOUT_MS) {
        setCheckoutPollTimedOut(true);
        return;
      }
      try {
        const data = await fetchSubscriptionOrder(activeCheckout.order.id);
        if (stopped) {
          return;
        }
        setActiveCheckout((current) => current ? { ...current, order: data.order } : current);
        if (data.order.status === "paid") {
          markOrderPaid(data.order, {
            setPaidOrder,
            setPendingOrder,
            setActiveCheckout,
            setCheckoutPollStartedAt,
            setCheckoutPollTimedOut,
          });
        }
      } catch {
        // Notify may arrive slightly later; keep polling while the dialog is open.
      }
    };
    void poll();
    const timer = window.setInterval(() => void poll(), CHECKOUT_POLL_INTERVAL_MS);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [activeCheckout?.order?.id, checkoutPollStartedAt]);

  if (isCheckingAuth || !session) {
    return (
      <div className="grid min-h-[calc(100vh-120px)] place-items-center">
        <div className="flex items-center gap-2 rounded-full border border-border bg-card px-4 py-2 text-sm text-muted-foreground shadow-sm">
          <LoaderCircle className="size-4 animate-spin" />
          正在读取订阅信息...
        </div>
      </div>
    );
  }

  const activeRemotePlans = (remoteConfig?.plans || []).filter((plan) => plan.enabled);
  const displayPlans = activeRemotePlans.length > 0 ? activeRemotePlans.map(planFromRemote) : fallbackPlans;
  const paymentReady = Boolean(remoteConfig?.enabled && remoteConfig.payment_ready);
  const primaryPayType = remoteConfig?.payment?.pay_types?.[0] || "alipay";

  const handleCheckout = async (planId: string) => {
    if (!paymentReady) {
      toast.error("订阅支付暂未配置，请联系管理员开通");
      return;
    }
    setCheckoutPlanId(planId);
    try {
      const checkout = await createSubscriptionCheckout(planId, primaryPayType);
      if (checkout.mode === "alipay_open" && checkout.qr_image_url) {
        setCheckoutPollStartedAt(Date.now());
        setCheckoutPollTimedOut(false);
        setActiveCheckout(checkout);
        return;
      }
      window.location.href = checkout.url;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "创建支付订单失败");
    } finally {
      setCheckoutPlanId("");
    }
  };

  return (
    <div className="relative isolate min-h-[calc(100vh-120px)] overflow-hidden rounded-[24px] bg-[#f5f7fb] px-3 py-4 text-slate-950 sm:rounded-[34px] sm:px-6 sm:py-6 lg:px-8">
      <Dialog
        open={Boolean(activeCheckout)}
        onOpenChange={(open) => {
          if (!open) {
            setActiveCheckout(null);
            setCheckoutPollTimedOut(false);
          }
        }}
      >
        <DialogContent className="max-h-[min(88dvh,680px)] w-[min(94vw,520px)] overflow-y-auto border-white/70 bg-white/95 p-0 text-slate-950 shadow-[0_32px_120px_rgba(15,23,42,0.22)] backdrop-blur-xl">
          <div className="rounded-[22px] bg-gradient-to-br from-sky-50 via-white to-emerald-50 p-4 sm:rounded-[24px] sm:p-6">
            <DialogHeader className="text-center">
              <Badge className="mx-auto rounded-full border-blue-100 bg-blue-50 px-3 py-1 text-blue-700">1818.pro 支付宝动态码</Badge>
              <DialogTitle className="mt-3 text-2xl font-black tracking-[-0.05em] sm:text-3xl">扫码完成付款</DialogTitle>
              <DialogDescription>请使用支付宝扫描下方二维码，支付成功后点数会自动到账。</DialogDescription>
            </DialogHeader>
            {activeCheckout ? (
              <div className="mt-4 rounded-[22px] border border-slate-200 bg-white p-3 sm:mt-5 sm:rounded-[24px] sm:p-4">
                <div className="grid gap-2 text-xs text-slate-600 sm:text-sm">
                  <div className="flex justify-between gap-4">
                    <span>套餐</span>
                    <span className="font-bold text-slate-950">{activeCheckout.order.plan_name}</span>
                  </div>
                  <div className="flex justify-between gap-4">
                    <span>订单号</span>
                    <span className="break-all text-right font-mono text-xs font-bold text-slate-950">{activeCheckout.order.id}</span>
                  </div>
                  <div className="flex justify-between gap-4">
                    <span>金额</span>
                    <span className="font-black text-rose-500">¥{activeCheckout.order.money}</span>
                  </div>
                </div>
                <div className="mx-auto mt-4 grid w-fit place-items-center rounded-[24px] bg-white p-3 shadow-[0_18px_60px_rgba(15,23,42,0.12)] sm:mt-5 sm:rounded-[28px] sm:p-4">
                  <img src={activeCheckout.qr_image_url} alt="支付宝支付二维码" className="size-[min(68vw,256px)] rounded-2xl" />
                </div>
                <div className="mt-4 flex items-center justify-center gap-2 rounded-2xl bg-blue-50 px-3 py-2.5 text-xs font-bold text-blue-700 sm:px-4 sm:py-3 sm:text-sm">
                  <RefreshCw className={cn("size-4", !checkoutPollTimedOut && "animate-spin")} />
                  {activeCheckout.order.status === "paid"
                    ? "支付已确认"
                    : checkoutPollTimedOut
                      ? "暂未收到回调，请稍后刷新订单状态"
                      : "正在等待支付宝回调确认"}
                </div>
              </div>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
      <div className="pointer-events-none absolute inset-0 -z-10 overflow-hidden">
        <div className="absolute -top-40 left-[-60px] h-[420px] w-[420px] rounded-full bg-sky-300/35 blur-3xl" />
        <div className="absolute top-0 right-[-140px] h-[520px] w-[520px] rounded-full bg-amber-200/50 blur-3xl" />
        <div className="absolute bottom-[-220px] left-1/2 h-[560px] w-[560px] -translate-x-1/2 rounded-full bg-emerald-200/28 blur-3xl" />
        <div className="absolute inset-0 bg-[linear-gradient(135deg,rgba(255,255,255,0.88),rgba(255,255,255,0.42)_44%,rgba(255,255,255,0.78))]" />
        <div className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-white to-transparent" />
      </div>

      <motion.header
        className="mx-auto max-w-3xl pb-4 pt-2 text-center sm:pb-8 sm:pt-3 lg:pb-10"
        initial={{ opacity: 0, y: 18 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.52, ease: [0.22, 1, 0.36, 1] }}
      >
        <Badge className="rounded-full border-white/70 bg-white/75 px-3.5 py-1.5 text-slate-700 shadow-sm backdrop-blur-xl">
          <Crown className="mr-2 size-3.5 text-amber-500" />
          订阅中心
        </Badge>
        <h1 className="mt-4 text-[2.15rem] font-black leading-[0.98] tracking-[-0.06em] text-balance sm:mt-5 sm:text-5xl lg:text-6xl">
          选择适合你的出图点数。
        </h1>
        <p className="mx-auto mt-3 max-w-2xl text-sm leading-6 text-slate-600 sm:mt-4 sm:text-base">
          多档订阅覆盖个人试用、稳定创作和商业团队。支付成功自动到账，失败任务不扣成功点数。
        </p>
        <div className="mt-3 flex flex-wrap justify-center gap-x-3 gap-y-1.5 text-xs text-slate-500 sm:mt-4 sm:gap-x-4 sm:gap-y-2 sm:text-sm">
          {isLoadingConfig ? <span>正在加载套餐...</span> : null}
          {!isLoadingConfig && paymentReady ? <span>当前支付方式：{payTypeLabel(primaryPayType)}</span> : null}
          {!isLoadingConfig && remoteConfig && !paymentReady ? <span>支付未配置，套餐展示可见，购买入口暂不可用。</span> : null}
          {paidOrder ? <span>最近到账：已支付，+{formatPointAmount(paidOrder.quota)} 点。</span> : null}
          {!paidOrder && pendingOrder ? <span>待确认订单：{pendingOrder.plan_name}，暂未到账。</span> : null}
        </div>
      </motion.header>

      <section className="mx-auto max-w-7xl pb-6 sm:pb-8">
        <div className="grid gap-3 md:grid-cols-2 sm:gap-4 xl:grid-cols-3 2xl:grid-cols-4 2xl:items-stretch">
          {displayPlans.map((plan, index) => {
            const accent = planAccentClass(plan);
            const remotePlan = activeRemotePlans[index];
            const isCheckingOut = Boolean(remotePlan && checkoutPlanId === remotePlan.id);
            return (
              <motion.article
                key={plan.name}
                className={cn("group relative flex min-h-[410px] flex-col overflow-hidden rounded-[24px] border p-4 backdrop-blur-2xl transition-shadow duration-300 sm:min-h-[505px] sm:rounded-[30px] sm:p-6", accent.shell)}
                initial={{ opacity: 0, y: 22 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.1 + index * 0.06, duration: 0.42, ease: [0.22, 1, 0.36, 1] }}
                whileHover={{ y: -5 }}
              >
                <div className={cn("pointer-events-none absolute inset-x-0 top-0 h-32 bg-gradient-to-b", accent.glow)} />
                <div className="pointer-events-none absolute inset-x-8 top-0 h-px bg-gradient-to-r from-transparent via-white to-transparent" />
                <div className="relative flex items-start justify-between gap-4">
                  <div>
                    <span className={cn("inline-flex rounded-full px-2.5 py-0.5 text-[10px] font-black shadow-sm sm:px-3 sm:py-1 sm:text-[11px]", accent.badge)}>{plan.tag}</span>
                    <h3 className="mt-3 text-2xl font-black tracking-[-0.06em] text-slate-950 sm:mt-4 sm:text-3xl">{plan.name}</h3>
                  </div>
                  {plan.featured ? (
                    <span className="grid size-9 place-items-center rounded-2xl bg-white text-[#1456f0] shadow-[0_10px_24px_rgba(15,23,42,0.11)] sm:size-10">
                      <Sparkles className="size-4" />
                    </span>
                  ) : null}
                </div>

                <div className="relative mt-4 flex items-end gap-1 sm:mt-6">
                  <span className="text-4xl font-black tracking-[-0.08em] sm:text-6xl">{plan.price}</span>
                  <span className="pb-1.5 text-xs font-semibold text-slate-500 sm:pb-2 sm:text-sm">{plan.unit}</span>
                </div>
                <p className="relative mt-3 min-h-0 text-sm leading-5 text-slate-600 sm:mt-4 sm:min-h-12 sm:leading-6">{plan.description}</p>

                <div className={cn("relative mt-4 rounded-[20px] px-3.5 py-3 sm:mt-5 sm:rounded-[22px] sm:px-4 sm:py-3.5", accent.quota)}>
                  <div className="text-[11px] font-black uppercase tracking-[0.16em] text-white/70">包含点数</div>
                  <div className="mt-1.5 flex flex-wrap items-end gap-2">
                    <span className="text-xs font-bold text-white/65 line-through decoration-2 decoration-rose-200">{plan.originalQuota}</span>
                    <span className="text-xl font-black tracking-[-0.05em] sm:text-2xl">{plan.quota}</span>
                  </div>
                </div>

                <ul className="relative mt-4 space-y-2 sm:mt-5 sm:space-y-2.5">
                  {plan.features.map((feature) => (
                    <li key={feature} className="flex items-start gap-2.5 text-xs leading-5 text-slate-700 sm:text-sm">
                      <span className="mt-0.5 grid size-5 shrink-0 place-items-center rounded-full bg-emerald-100 text-emerald-700">
                        <Check className="size-3" />
                      </span>
                      <span>{feature}</span>
                    </li>
                  ))}
                </ul>

                <Button
                  type="button"
                  className={cn("relative mt-4 h-10 w-full rounded-full text-sm font-black sm:mt-auto sm:h-11", accent.button)}
                  onClick={() => remotePlan ? void handleCheckout(remotePlan.id) : toast.error("套餐暂未配置")}
                  disabled={!remotePlan || isCheckingOut}
                >
                  {isCheckingOut ? "正在创建订单..." : paymentReady ? "立即购买" : "联系管理员开通"}
                  <ArrowRight className="size-4" />
                </Button>
              </motion.article>
            );
          })}
        </div>
      </section>
    </div>
  );
}
