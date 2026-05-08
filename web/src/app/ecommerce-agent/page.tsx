import { motion } from "motion/react";

export default function EcommerceAgentPage() {
  return (
    <div className="flex min-h-[calc(100vh-120px)] w-full flex-col items-center justify-center gap-10 p-4">
      {/* Animation Container */}
      <div className="relative h-64 w-64">
        {/* Desk and Computer */}
        <svg
          viewBox="0 0 200 200"
          className="absolute inset-0 h-full w-full"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          {/* Desk Line */}
          <path
            d="M 20 160 L 180 160"
            stroke="currentColor"
            strokeWidth="8"
            strokeLinecap="round"
            className="text-border"
          />

          {/* Laptop Base */}
          <path
            d="M 90 156 L 150 156"
            stroke="currentColor"
            strokeWidth="8"
            strokeLinecap="round"
            className="text-foreground/80"
          />

          {/* Laptop Screen */}
          <path
            d="M 140 156 L 110 100"
            stroke="currentColor"
            strokeWidth="10"
            strokeLinecap="round"
            className="text-foreground/80"
          />

          {/* Person Head */}
          <circle
            cx="60"
            cy="70"
            r="22"
            fill="currentColor"
            className="text-primary"
          />

          {/* Person Body */}
          <path
            d="M 60 90 L 60 160"
            stroke="currentColor"
            strokeWidth="32"
            strokeLinecap="round"
            className="text-primary"
          />
        </svg>

        {/* Left Arm Typing */}
        <svg
          viewBox="0 0 200 200"
          className="pointer-events-none absolute inset-0 h-full w-full"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <motion.path
            d="M 60 105 L 100 150"
            stroke="currentColor"
            strokeWidth="14"
            strokeLinecap="round"
            className="text-primary"
            style={{ originX: "60px", originY: "105px" }}
            animate={{ rotateZ: [0, -15, 0] }}
            transition={{ duration: 0.3, repeat: Infinity, ease: "easeInOut" }}
          />
        </svg>

        {/* Right Arm Typing */}
        <svg
          viewBox="0 0 200 200"
          className="pointer-events-none absolute inset-0 h-full w-full"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <motion.path
            d="M 60 105 L 110 145"
            stroke="currentColor"
            strokeWidth="14"
            strokeLinecap="round"
            className="text-primary/90"
            style={{ originX: "60px", originY: "105px" }}
            animate={{ rotateZ: [5, -5, 5] }}
            transition={{ duration: 0.4, repeat: Infinity, ease: "easeInOut" }}
          />
        </svg>

        {/* Coffee cup */}
        <svg
          viewBox="0 0 200 200"
          className="absolute inset-0 h-full w-full"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <path
            d="M 40 156 L 40 135 C 40 135, 50 135, 50 145 C 50 155, 40 156, 40 156 Z"
            stroke="currentColor"
            strokeWidth="4"
            className="text-foreground/50"
          />
          <path
            d="M 30 156 L 40 156 L 40 135 L 30 135 Z"
            fill="currentColor"
            className="text-foreground/50"
          />
        </svg>

        {/* Steam from coffee */}
        <motion.div
          className="absolute bottom-[70px] left-[32px] text-foreground/40"
          animate={{ opacity: [0, 1, 0], y: [0, -15] }}
          transition={{ duration: 1.5, repeat: Infinity, ease: "easeOut" }}
        >
          <svg
            width="10"
            height="20"
            viewBox="0 0 10 20"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            xmlns="http://www.w3.org/2000/svg"
          >
            <path d="M 3 20 Q 8 15 3 10 Q -2 5 3 0" />
          </svg>
        </motion.div>

        {/* Moon/Night element (working late) */}
        <motion.div
          className="absolute left-[130px] top-[40px] text-amber-400 dark:text-amber-300"
          animate={{ rotate: 360 }}
          transition={{ duration: 20, repeat: Infinity, ease: "linear" }}
        >
          <svg
            width="30"
            height="30"
            viewBox="0 0 24 24"
            fill="currentColor"
            stroke="none"
            xmlns="http://www.w3.org/2000/svg"
          >
            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
          </svg>
        </motion.div>

        {/* Zzz Text (Tired) */}
        <motion.div
          className="absolute left-[20px] top-[10px] text-2xl font-bold text-indigo-500 dark:text-indigo-400"
          animate={{ opacity: [0, 1, 0], y: [0, -20, -40], x: [0, -10, -20] }}
          transition={{ duration: 2.5, repeat: Infinity, ease: "easeOut" }}
        >
          Z
        </motion.div>
        <motion.div
          className="absolute left-[10px] top-[0px] text-lg font-bold text-indigo-400 dark:text-indigo-300"
          animate={{ opacity: [0, 1, 0], y: [0, -20, -40], x: [0, -10, -20] }}
          transition={{ duration: 2.5, repeat: Infinity, ease: "easeOut", delay: 0.8 }}
        >
          z
        </motion.div>
      </div>

      {/* Text Section */}
      <motion.div
        className="flex flex-col items-center gap-3 text-center"
        initial={{ opacity: 0, y: 15 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.6, delay: 0.2 }}
      >
        <h2 className="text-3xl font-bold tracking-tight text-foreground sm:text-4xl">
          正在熬夜开发中....
        </h2>
        <p className="max-w-[500px] text-lg text-muted-foreground">
          全新的「电商 AI-Agent」功能正在加紧打造中，敬请期待！
        </p>
      </motion.div>
    </div>
  );
}
