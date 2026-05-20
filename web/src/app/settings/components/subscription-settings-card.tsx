"use client";

import { useEffect, useRef, useState } from "react";
import { Crown, LoaderCircle, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  fetchAdminSubscription,
  updateAdminSubscription,
  type SubscriptionConfig,
  type SubscriptionOrder,
  type SubscriptionPlan,
} from "@/lib/api";
import { cn } from "@/lib/utils";

import {
  SettingsCard,
  SettingsEmptyState,
  SettingsNotice,
  settingsInputClassName,
  settingsListItemClassName,
  settingsToggleClassName,
} from "./settings-ui";

const defaultPlan: SubscriptionPlan = {
  id: "new-plan",
  name: "新套餐",
  description: "",
  price: "19.00",
  quota: 100,
  enabled: true,
  recommended: false,
};

function normalizePlans(plans: SubscriptionPlan[]) {
  return plans.map((plan, index) => ({
    ...plan,
    id: String(plan.id || `plan-${index + 1}`).trim(),
    name: String(plan.name || `套餐 ${index + 1}`).trim(),
    description: String(plan.description || "").trim(),
    price: String(plan.price || "0").trim(),
    quota: Math.max(0, Number(plan.quota) || 0),
    enabled: Boolean(plan.enabled),
    recommended: Boolean(plan.recommended),
  }));
}

function formatDateTime(value?: string) {
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

function payTypeLabel(payType: string) {
  switch (payType) {
    case "alipay":
      return "支付宝";
    case "wxpay":
      return "微信";
    case "qqpay":
      return "QQ";
    default:
      return payType || "未知";
  }
}

function paymentModeLabel(mode?: string) {
  return mode === "alipay_open" ? "支付宝开放平台动态码" : "码支付 / 易支付";
}

function orderStatusBadge(order: SubscriptionOrder) {
  if (order.status === "paid" && order.quota_granted) {
    return { label: "已到账", variant: "success" as const };
  }
  if (order.status === "paid") {
    return { label: "已支付待入账", variant: "warning" as const };
  }
  return { label: "待支付", variant: "warning" as const };
}

function orderQuotaText(order: SubscriptionOrder) {
  if (order.status !== "paid") {
    return `待支付：${order.quota} 点`;
  }
  if (!order.quota_granted) {
    return `待入账：${order.quota} 点`;
  }
  return `已到账：+${order.quota} 点`;
}

function shortOrderId(value?: string) {
  const text = String(value || "").trim();
  if (!text) {
    return "—";
  }
  return text.length > 12 ? `...${text.slice(-8)}` : text;
}

export function SubscriptionSettingsCard() {
  const didLoadRef = useRef(false);
  const [config, setConfig] = useState<SubscriptionConfig | null>(null);
  const [orders, setOrders] = useState<SubscriptionOrder[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);

  const load = async () => {
    setIsLoading(true);
    try {
      const data = await fetchAdminSubscription();
      setConfig(data.config);
      setOrders(data.orders || []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载订阅设置失败");
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

  const updateConfig = (updates: Partial<SubscriptionConfig>) => {
    setConfig((current) => current ? { ...current, ...updates } : current);
  };

  const updatePayment = (updates: Partial<SubscriptionConfig["payment"]>) => {
    setConfig((current) => current ? { ...current, payment: { ...current.payment, ...updates } } : current);
  };

  const updatePlan = (index: number, updates: Partial<SubscriptionPlan>) => {
    setConfig((current) => {
      if (!current) {
        return current;
      }
      const plans = [...current.plans];
      plans[index] = { ...plans[index], ...updates };
      return { ...current, plans };
    });
  };

  const addPlan = () => {
    setConfig((current) => {
      if (!current) {
        return current;
      }
      const id = `plan-${current.plans.length + 1}`;
      return { ...current, plans: [...current.plans, { ...defaultPlan, id }] };
    });
  };

  const deletePlan = (index: number) => {
    setConfig((current) => {
      if (!current) {
        return current;
      }
      const plans = current.plans.filter((_, itemIndex) => itemIndex !== index);
      return { ...current, plans: plans.length > 0 ? plans : current.plans };
    });
  };

  const togglePayType = (payType: string, checked: boolean) => {
    const current = config?.payment.pay_types || [];
    const next = checked ? [...new Set([...current, payType])] : current.filter((item) => item !== payType);
    updatePayment({ pay_types: next });
  };

  const save = async () => {
    if (!config) {
      return;
    }
    if (!config.plans.some((plan) => plan.enabled)) {
      toast.error("至少开启一个套餐");
      return;
    }
    const payload: SubscriptionConfig = {
      ...config,
      plans: normalizePlans(config.plans),
      payment: {
        ...config.payment,
        mode: config.payment.mode || "epay",
        gateway_url: String(config.payment.gateway_url || "").trim(),
        merchant_id: String(config.payment.merchant_id || "").trim(),
        merchant_key: String(config.payment.merchant_key || "").trim(),
        app_private_key: String(config.payment.app_private_key || "").trim(),
        alipay_public_key: String(config.payment.alipay_public_key || "").trim(),
        pay_types: config.payment.pay_types?.length ? config.payment.pay_types : ["alipay"],
        site_name: String(config.payment.site_name || "1818.pro").trim(),
      },
    };
    setIsSaving(true);
    try {
      const data = await updateAdminSubscription(payload);
      setConfig(data.config);
      setOrders(data.orders || []);
      toast.success("订阅设置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存订阅设置失败");
    } finally {
      setIsSaving(false);
    }
  };

  if (isLoading) {
    return (
      <SettingsCard icon={Crown} title="订阅设置" description="配置套餐和码支付收款。" tone="amber">
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  if (!config) {
    return (
      <SettingsCard icon={Crown} title="订阅设置" description="配置套餐和码支付收款。" tone="amber">
        <SettingsEmptyState icon={Crown} title="订阅配置不可用" description="刷新页面后重试。" />
      </SettingsCard>
    );
  }

  const isAlipayOpen = (config.payment.mode || "epay") === "alipay_open";
  const paymentReady = isAlipayOpen
    ? Boolean(config.payment.app_private_key_configured && config.payment.alipay_public_key_configured && config.payment.gateway_url && config.payment.merchant_id)
    : Boolean(config.payment.merchant_key_configured && config.payment.gateway_url && config.payment.merchant_id);

  return (
    <SettingsCard
      icon={Crown}
      title="订阅设置"
      description={`配置套餐和${paymentModeLabel(config.payment.mode)}收款。`}
      tone="amber"
      meta={<Badge variant={paymentReady ? "success" : "warning"}>{paymentReady ? "支付已配置" : "支付未完整"}</Badge>}
      action={
        <Button size="lg" onClick={() => void save()} disabled={isSaving}>
          {isSaving ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <Save data-icon="inline-start" />}
          保存
        </Button>
      }
    >
      <div className="flex flex-col gap-5">
        <label className={settingsToggleClassName}>
          <Checkbox checked={Boolean(config.enabled)} onCheckedChange={(value) => updateConfig({ enabled: Boolean(value) })} />
          <span>开启订阅页面购买入口</span>
        </label>

        <section className="flex flex-col gap-3">
          <div>
            <h3 className="text-sm font-semibold text-foreground">支付对接</h3>
            <p className="mt-1 text-xs text-muted-foreground">
              支持原码支付 / 易支付 submit.php，也支持支付宝开放平台 alipay.trade.precreate 动态码。密钥留空表示不修改。
            </p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className={settingsToggleClassName}>
              <Checkbox checked={Boolean(config.payment.enabled)} onCheckedChange={(value) => updatePayment({ enabled: Boolean(value) })} />
              <span>启用支付</span>
            </label>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="subscription-payment-mode">支付模式</FieldLabel>
              <select
                id="subscription-payment-mode"
                value={String(config.payment.mode || "epay")}
                onChange={(event) => updatePayment({
                  mode: event.target.value,
                  gateway_url: event.target.value === "alipay_open" && !config.payment.gateway_url
                    ? "https://openapi.alipay.com/gateway.do"
                    : config.payment.gateway_url,
                  pay_types: event.target.value === "alipay_open" ? ["alipay"] : config.payment.pay_types,
                })}
                className={cn(settingsInputClassName, "h-11")}
              >
                <option value="epay">码支付 / 易支付</option>
                <option value="alipay_open">支付宝开放平台动态码</option>
              </select>
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="subscription-site-name">站点名称</FieldLabel>
              <Input
                id="subscription-site-name"
                value={String(config.payment.site_name || "")}
                onChange={(event) => updatePayment({ site_name: event.target.value })}
                placeholder="1818.pro"
                className={settingsInputClassName}
              />
            </Field>
            <Field className="gap-1.5 sm:col-span-2">
              <FieldLabel htmlFor="subscription-gateway">网关地址</FieldLabel>
              <Input
                id="subscription-gateway"
                value={String(config.payment.gateway_url || "")}
                onChange={(event) => updatePayment({ gateway_url: event.target.value })}
                placeholder={isAlipayOpen ? "https://openapi.alipay.com/gateway.do" : "https://pay.example.com 或 https://pay.example.com/submit.php"}
                className={settingsInputClassName}
              />
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="subscription-merchant-id">{isAlipayOpen ? "支付宝 APPID" : "商户 ID / PID"}</FieldLabel>
              <Input
                id="subscription-merchant-id"
                value={String(config.payment.merchant_id || "")}
                onChange={(event) => updatePayment({ merchant_id: event.target.value })}
                placeholder={isAlipayOpen ? "202100xxxxxxxxxx" : "1000"}
                className={settingsInputClassName}
              />
            </Field>
            {!isAlipayOpen ? (
              <Field className="gap-1.5">
                <FieldLabel htmlFor="subscription-merchant-key">
                  商户密钥 {config.payment.merchant_key_configured ? "（已配置）" : ""}
                </FieldLabel>
                <Input
                  id="subscription-merchant-key"
                  type="password"
                  value={String(config.payment.merchant_key || "")}
                  onChange={(event) => updatePayment({ merchant_key: event.target.value })}
                  placeholder={config.payment.merchant_key_configured ? "留空不修改" : "请输入商户密钥"}
                  className={settingsInputClassName}
                />
              </Field>
            ) : null}
            {isAlipayOpen ? (
              <>
                <Field className="gap-1.5 sm:col-span-2">
                  <FieldLabel htmlFor="subscription-app-private-key">
                    应用私钥 {config.payment.app_private_key_configured ? "（已配置）" : ""}
                  </FieldLabel>
                  <Textarea
                    id="subscription-app-private-key"
                    value={String(config.payment.app_private_key || "")}
                    onChange={(event) => updatePayment({ app_private_key: event.target.value })}
                    placeholder={config.payment.app_private_key_configured ? "留空不修改" : "粘贴支付宝应用私钥"}
                    className={cn(settingsInputClassName, "min-h-28 resize-y font-mono text-xs")}
                  />
                </Field>
                <Field className="gap-1.5 sm:col-span-2">
                  <FieldLabel htmlFor="subscription-alipay-public-key">
                    支付宝公钥 {config.payment.alipay_public_key_configured ? "（已配置）" : ""}
                  </FieldLabel>
                  <Textarea
                    id="subscription-alipay-public-key"
                    value={String(config.payment.alipay_public_key || "")}
                    onChange={(event) => updatePayment({ alipay_public_key: event.target.value })}
                    placeholder={config.payment.alipay_public_key_configured ? "留空不修改" : "粘贴支付宝公钥"}
                    className={cn(settingsInputClassName, "min-h-24 resize-y font-mono text-xs")}
                  />
                </Field>
              </>
            ) : null}
          </div>
          <div className="grid gap-2 sm:grid-cols-3">
            {["alipay", "wxpay", "qqpay"].map((payType) => (
              <label key={payType} className={settingsToggleClassName}>
                <Checkbox
                  checked={(config.payment.pay_types || []).includes(payType)}
                  onCheckedChange={(value) => togglePayType(payType, Boolean(value))}
                  disabled={isAlipayOpen && payType !== "alipay"}
                />
                <span>{payTypeLabel(payType)}</span>
              </label>
            ))}
          </div>
          <SettingsNotice>
            异步回调地址：<code className="font-mono">/api/subscription/payment/notify</code>。支付宝开放平台模式会生成每笔订单的动态二维码，支付成功后验签、去重并自动给用户加点数。
          </SettingsNotice>
        </section>

        <section className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-foreground">套餐配置</h3>
              <p className="mt-1 text-xs text-muted-foreground">价格单位元，点数为成功购买后增加的图片点数。</p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={addPlan}>
              <Plus data-icon="inline-start" />
              添加套餐
            </Button>
          </div>
          <div className="flex flex-col gap-3">
            {config.plans.map((plan, index) => (
              <div key={`${plan.id}-${index}`} className={settingsListItemClassName}>
                <div className="mb-3 flex items-center justify-between gap-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={plan.enabled ? "success" : "secondary"}>{plan.enabled ? "启用" : "停用"}</Badge>
                    {plan.recommended ? <Badge variant="warning">推荐</Badge> : null}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => deletePlan(index)}
                    disabled={config.plans.length <= 1}
                    aria-label={`删除套餐 ${plan.name || plan.id || index + 1}`}
                    title="删除套餐"
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field className="gap-1.5">
                    <FieldLabel>套餐 ID</FieldLabel>
                    <Input value={plan.id} onChange={(event) => updatePlan(index, { id: event.target.value })} className={settingsInputClassName} />
                  </Field>
                  <Field className="gap-1.5">
                    <FieldLabel>套餐名称</FieldLabel>
                    <Input value={plan.name} onChange={(event) => updatePlan(index, { name: event.target.value })} className={settingsInputClassName} />
                  </Field>
                  <Field className="gap-1.5">
                    <FieldLabel>价格</FieldLabel>
                    <Input
                      type="number"
                      min={0}
                      step="0.01"
                      value={String(plan.price)}
                      onChange={(event) => updatePlan(index, { price: event.target.value })}
                      className={settingsInputClassName}
                    />
                  </Field>
                  <Field className="gap-1.5">
                    <FieldLabel>增加点数</FieldLabel>
                    <Input
                      type="number"
                      min={0}
                      value={String(plan.quota)}
                      onChange={(event) => updatePlan(index, { quota: Number(event.target.value) || 0 })}
                      className={settingsInputClassName}
                    />
                  </Field>
                  <Field className="gap-1.5 sm:col-span-2">
                    <FieldLabel>说明</FieldLabel>
                    <Input
                      value={String(plan.description || "")}
                      onChange={(event) => updatePlan(index, { description: event.target.value })}
                      placeholder="适合低频创作 / 商业团队等"
                      className={settingsInputClassName}
                    />
                  </Field>
                </div>
                <div className="mt-3 grid gap-2 sm:grid-cols-2">
                  <label className={settingsToggleClassName}>
                    <Checkbox checked={Boolean(plan.enabled)} onCheckedChange={(value) => updatePlan(index, { enabled: Boolean(value) })} />
                    <span>启用套餐</span>
                  </label>
                  <label className={settingsToggleClassName}>
                    <Checkbox checked={Boolean(plan.recommended)} onCheckedChange={(value) => updatePlan(index, { recommended: Boolean(value) })} />
                    <span>设为推荐</span>
                  </label>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold text-foreground">最近订单</h3>
          {orders.length === 0 ? (
            <SettingsEmptyState icon={Crown} title="暂无订阅订单" description="用户购买后会显示在这里。" />
          ) : (
            <div className="flex flex-col gap-2">
              {orders.slice(0, 8).map((order) => (
                <div key={order.id} className={cn(settingsListItemClassName, "flex flex-col gap-2")}>
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0 truncate text-sm font-semibold text-foreground">{order.plan_name}</div>
                    <Badge variant={orderStatusBadge(order).variant}>{orderStatusBadge(order).label}</Badge>
                  </div>
                  <div className="grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
                    <span className="truncate">用户：{order.owner_name || order.owner_id || "—"}</span>
                    <span>金额：¥{order.money} / {payTypeLabel(order.pay_type)}</span>
                    <span>{orderQuotaText(order)}</span>
                    <span>创建：{formatDateTime(order.created_at)}</span>
                    <span className="truncate">订单：{shortOrderId(order.id)}</span>
                    <span className="truncate">流水：{shortOrderId(order.trade_no)}</span>
                    <span>支付：{formatDateTime(order.paid_at)}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </SettingsCard>
  );
}
