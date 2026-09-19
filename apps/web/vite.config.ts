import { defineConfig, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";

const coreUrl =
  (globalThis as { process?: { env?: Record<string, string | undefined> } })
    .process?.env?.REVIEW_STUDIO_CORE_URL ?? "http://127.0.0.1:8787";
const webHost =
  (globalThis as { process?: { env?: Record<string, string | undefined> } })
    .process?.env?.REVIEW_STUDIO_WEB_HOST ?? "127.0.0.1";

type ProxyRequest = {
  socket: {
    remoteAddress?: string;
  };
};

type ProxyClientRequest = {
  setHeader(name: string, value: string): void;
};

type ProxyEventTarget = {
  on(
    eventName: "proxyReq",
    listener: (proxyReq: ProxyClientRequest, request: ProxyRequest) => void,
  ): void;
};

function coreProxy(): ProxyOptions {
  return {
    target: coreUrl,
    changeOrigin: false,
    configure(proxy) {
      (proxy as unknown as ProxyEventTarget).on(
        "proxyReq",
        (proxyReq, request) => {
          const remoteAddress = request.socket.remoteAddress ?? "";
          proxyReq.setHeader("X-Forwarded-For", remoteAddress);
          proxyReq.setHeader("X-Real-IP", remoteAddress);
        },
      );
    },
  };
}

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (
            id.includes("node_modules/react/") ||
            id.includes("node_modules/react-dom/")
          ) {
            return "react-vendor";
          }
          if (id.includes("node_modules/hls.js/")) {
            return "hls";
          }
          if (id.includes("node_modules/lucide-react/")) {
            return "icons";
          }
          return undefined;
        },
      },
    },
  },
  server: {
    host: webHost,
    port: 5173,
    proxy: {
      "/api": coreProxy(),
      "/health": coreProxy(),
      "/share-api": coreProxy(),
      "/join-api": coreProxy(),
    },
  },
});
