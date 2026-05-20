import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const webRoot = path.dirname(fileURLToPath(import.meta.url));
const appVersion = process.env.VITE_APP_VERSION || process.env.npm_package_version || "0.0.0-dev";

export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  resolve: {
    alias: {
      "@": path.resolve(webRoot, "src"),
    },
  },
  server: {
    host: "0.0.0.0",
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("/react/") || id.includes("/react-dom/") || id.includes("/scheduler/")) {
            return "vendor-react";
          }
          if (id.includes("@radix-ui")) return "vendor-radix";
          if (id.includes("lucide-react")) return "vendor-lucide";
          if (
            id.includes("react-markdown") ||
            id.includes("remark") ||
            id.includes("rehype") ||
            id.includes("micromark") ||
            id.includes("hast") ||
            id.includes("mdast") ||
            id.includes("unified") ||
            id.includes("unist")
          ) {
            return "vendor-markdown";
          }
          if (id.includes("zustand") || id.includes("immer")) return "vendor-state";
          if (id.includes("axios") || id.includes("ky/")) return "vendor-http";
          if (id.includes("recharts") || id.includes("d3-")) return "vendor-charts";
          return "vendor";
        },
      },
    },
  },
});
