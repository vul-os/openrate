import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Served at the binary root, so base "/". The dev server proxies /api and
// /healthz to a locally running openrate binary on :8080.
export default defineConfig({
  base: "/",
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
});
