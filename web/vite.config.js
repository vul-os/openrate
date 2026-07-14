import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Served at the binary root, so base "/". The dev server proxies /api and
// /healthz to a locally running openrate binary on :8080.
export default defineConfig({
  base: "/",
  plugins: [react()],
  // Preserve the licence banners upstream packages put in their own source (e.g.
  // React's "@license React" header). Vite 8 minifies JS with oxc, whose
  // output.comments.legal defaults to FALSE while minifying — so a default
  // production build silently strips the very attribution MIT requires us to
  // keep. Turning it back on means the shipped bundle carries those banners;
  // the generated notices file served at /licenses.txt covers the rest.
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: { output: { comments: { legal: true } } },
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
});
