import { motion } from "motion/react";

export default function EcommerceAgentPage() {
  return (
    <div className="flex min-h-[calc(100vh-120px)] w-full flex-col items-center justify-center p-4">
      <motion.div
        className="flex w-full max-w-2xl flex-col items-center gap-8 overflow-hidden rounded-[24px] border border-border bg-card p-6 shadow-sm dark:border-border dark:bg-card/90 sm:p-10"
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.6 }}
      >
        {/* Anime GIF Container */}
        <div className="relative aspect-video w-full overflow-hidden rounded-2xl bg-black/5 dark:bg-white/5 shadow-inner">
          <img
            src="/ecommerce-agent.gif"
            alt="Anime developer working late"
            className="h-full w-full object-cover"
            loading="lazy"
          />
        </div>

        {/* Text Section */}
        <div className="flex flex-col items-center gap-3 text-center">
          <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
            正在熬夜开发中....
          </h2>
          <p className="max-w-[500px] text-lg leading-relaxed text-muted-foreground">
            全新的「电商 AI-Agent」功能正在加紧打造中，敬请期待！
          </p>
        </div>
      </motion.div>
    </div>
  );
}
