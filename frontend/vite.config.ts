import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

// The Go server (cmd/serve) listens on :8080 by default. In dev we run Vite's
// HMR server and proxy the JSON API to the Go process, so the frontend stays a
// thin client over the same endpoints it hits in production. Override the target
// with VITE_API_TARGET (e.g. when serve runs on a different AIL_ADDR).
const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

// Production build lands in internal/server/static so `//go:embed static` bakes
// it into the binary. Fixed (unhashed) asset names keep the committed bundle's
// diffs small across rebuilds.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("../internal/server/static", import.meta.url)),
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "assets/app.js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name][extname]",
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
    },
  },
});
