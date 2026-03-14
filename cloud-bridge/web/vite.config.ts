import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  // 管理页面通过 Bridge 的 /admin 路由对外暴露，静态资源统一带该前缀。
  base: "/admin/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // 输出 manifest 供内嵌版本计算与排障定位使用。
    manifest: "manifest.json",
    // 显式固定带 hash 的产物命名，便于服务端 immutable 缓存策略。
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
});
