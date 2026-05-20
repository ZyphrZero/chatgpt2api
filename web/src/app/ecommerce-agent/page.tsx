import { ImagePlus, Layers3, PackageOpen, Sparkles, WandSparkles } from "lucide-react";
import { motion } from "motion/react";

const buildSteps = [
  { label: "商品图上传", icon: ImagePlus },
  { label: "主图模板生成", icon: WandSparkles },
  { label: "详情页套版", icon: Layers3 },
];

const floatingCards = [
  { title: "主图", detail: "场景光影", className: "left-[8%] top-[18%] rotate-[-7deg]" },
  { title: "详情页", detail: "卖点拆解", className: "right-[8%] top-[24%] rotate-[6deg]" },
  { title: "种草图", detail: "平台适配", className: "bottom-[12%] left-[14%] rotate-[5deg]" },
  { title: "批量导出", detail: "多尺寸", className: "bottom-[18%] right-[13%] rotate-[-6deg]" },
];

export default function EcommerceAgentPage() {
  return (
    <div className="relative isolate min-h-[calc(100vh-120px)] overflow-hidden rounded-[34px] bg-[#f5f7fb] px-4 py-8 text-slate-950 sm:px-6 lg:px-8">
      <div className="pointer-events-none absolute inset-0 -z-10 overflow-hidden">
        <div className="absolute -top-40 left-[-80px] h-[460px] w-[460px] rounded-full bg-sky-300/35 blur-3xl" />
        <div className="absolute top-10 right-[-140px] h-[520px] w-[520px] rounded-full bg-amber-200/45 blur-3xl" />
        <div className="absolute bottom-[-220px] left-1/2 h-[560px] w-[560px] -translate-x-1/2 rounded-full bg-emerald-200/25 blur-3xl" />
        <div className="absolute inset-0 bg-[linear-gradient(135deg,rgba(255,255,255,0.9),rgba(255,255,255,0.42)_45%,rgba(255,255,255,0.78))]" />
      </div>

      <motion.section
        className="relative mx-auto grid min-h-[calc(100vh-185px)] max-w-6xl place-items-center py-4"
        initial={{ opacity: 0, y: 18 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.48, ease: [0.22, 1, 0.36, 1] }}
      >
        <div className="relative w-full overflow-hidden rounded-[38px] border border-white/75 bg-white/58 p-5 shadow-[0_28px_90px_rgba(40,55,90,0.13)] backdrop-blur-3xl sm:p-8 lg:p-10">
          <div className="pointer-events-none absolute inset-x-10 top-0 h-px bg-gradient-to-r from-transparent via-white to-transparent" />
          <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_20%,rgba(59,130,246,0.12),transparent_35%),radial-gradient(circle_at_52%_78%,rgba(245,158,11,0.13),transparent_38%)]" />

          <div className="relative mx-auto flex max-w-4xl flex-col items-center text-center">
            <motion.div
              className="inline-flex items-center gap-2 rounded-full border border-white/80 bg-white/75 px-4 py-2 text-sm font-black text-slate-700 shadow-sm backdrop-blur-xl"
              animate={{ y: [0, -3, 0] }}
              transition={{ duration: 2.2, repeat: Infinity, ease: "easeInOut" }}
            >
              <Sparkles className="size-4 text-blue-600" />
              电商 AI-Agent
            </motion.div>

            <h1 className="mt-7 text-4xl font-black leading-[0.98] tracking-[-0.07em] text-balance sm:text-6xl lg:text-7xl">
              正在开发中
              <motion.span
                className="inline-block"
                animate={{ opacity: [0.25, 1, 0.25] }}
                transition={{ duration: 1.1, repeat: Infinity, ease: "easeInOut" }}
              >
                ...
              </motion.span>
            </h1>
            <p className="mt-5 max-w-2xl text-base leading-7 text-slate-600 sm:text-lg">
              上传随手拍商品图，一键生成主图、详情页、种草图和多平台素材的完整工作台正在打磨中。
            </p>

            <div className="relative mt-10 grid size-[250px] place-items-center sm:size-[310px]">
              <motion.div
                className="absolute inset-0 rounded-full border border-dashed border-blue-300/70"
                animate={{ rotate: 360 }}
                transition={{ duration: 18, repeat: Infinity, ease: "linear" }}
              />
              <motion.div
                className="absolute inset-8 rounded-full border border-dashed border-amber-300/70"
                animate={{ rotate: -360 }}
                transition={{ duration: 14, repeat: Infinity, ease: "linear" }}
              />
              <motion.div
                className="grid size-32 place-items-center rounded-[34px] bg-gradient-to-br from-slate-950 via-slate-800 to-blue-600 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.32),0_30px_70px_rgba(15,23,42,0.28)] sm:size-40"
                animate={{ y: [0, -8, 0], scale: [1, 1.03, 1] }}
                transition={{ duration: 2.8, repeat: Infinity, ease: "easeInOut" }}
              >
                <PackageOpen className="size-16 sm:size-20" />
              </motion.div>

              {floatingCards.map((card, index) => (
                <motion.div
                  key={card.title}
                  className={`absolute hidden rounded-2xl border border-white/75 bg-white/70 px-4 py-3 text-left shadow-[0_16px_38px_rgba(15,23,42,0.11)] backdrop-blur-xl sm:block ${card.className}`}
                  animate={{ y: [0, index % 2 === 0 ? -7 : 7, 0] }}
                  transition={{ duration: 2.6 + index * 0.25, repeat: Infinity, ease: "easeInOut" }}
                >
                  <div className="text-sm font-black text-slate-950">{card.title}</div>
                  <div className="mt-0.5 text-xs font-semibold text-slate-500">{card.detail}</div>
                </motion.div>
              ))}
            </div>

            <div className="mt-8 w-full max-w-2xl rounded-[26px] border border-white/75 bg-white/70 p-4 shadow-sm backdrop-blur-2xl">
              <div className="mb-3 flex items-center justify-between text-xs font-bold text-slate-500">
                <span>开发进度</span>
                <span>模块联调中</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-slate-200/70">
                <motion.div
                  className="h-full rounded-full bg-gradient-to-r from-blue-600 via-sky-400 to-amber-400"
                  initial={{ width: "28%" }}
                  animate={{ width: ["28%", "76%", "48%", "86%"] }}
                  transition={{ duration: 4.2, repeat: Infinity, ease: "easeInOut" }}
                />
              </div>
              <div className="mt-4 grid gap-2 sm:grid-cols-3">
                {buildSteps.map((step, index) => (
                  <motion.div
                    key={step.label}
                    className="flex items-center justify-center gap-2 rounded-2xl bg-slate-950/[0.04] px-3 py-2 text-sm font-bold text-slate-700"
                    animate={{ opacity: [0.55, 1, 0.55] }}
                    transition={{ duration: 1.8, delay: index * 0.25, repeat: Infinity, ease: "easeInOut" }}
                  >
                    <step.icon className="size-4 text-blue-600" />
                    {step.label}
                  </motion.div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </motion.section>
    </div>
  );
}
