import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go server (cmd/serve) is API-only and listens on :8080 by default. In dev
// we run Vite's HMR server and proxy the JSON API to the Go process, so the
// frontend stays a thin client over those endpoints. Override the target with
// VITE_API_TARGET (e.g. when serve runs on a different AIL_ADDR).
const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8080";

// Production build lands in frontend/dist (served by any static host or
// `vite preview`). Hashed asset names give proper cache-busting — the Go binary
// no longer embeds the bundle, so there is no committed artifact to keep small.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
    },
  },
  // `vite preview` (prod bundle) uses its own proxy block.
  preview: {
    port: 4173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
    },
  },
});
