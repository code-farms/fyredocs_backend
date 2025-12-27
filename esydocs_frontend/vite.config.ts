import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, rootDir, "");
  const apiTarget = env.VITE_API_BASE_URL ?? "http://localhost:5000";

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@": path.resolve(rootDir, "src"),
        "@shared": path.resolve(rootDir, "../esydocs_backend/shared"),
        "@assets": path.resolve(rootDir, "attached_assets"),
      },
    },
    root: rootDir,
    build: {
      outDir: path.resolve(rootDir, "dist"),
      emptyOutDir: true,
    },
    server: {
      fs: {
        strict: true,
        deny: ["**/.*"],
      },
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          secure: false,
        },
      },
    },
    define: {
      __API_BASE_URL__: JSON.stringify(apiTarget),
    },
  };
});
