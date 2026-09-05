import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// fulfillment-mfe: the fulfillment-execution remote. Exposes ./App -- the
// shell lazy-loads it at /fulfillment/*. Also runnable standalone on :5184
// for local development without the shell (see main.tsx).
export default defineConfig({
  plugins: [
    react(),
    federation({
      name: "fulfillment_mfe",
      filename: "remoteEntry.js",
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.2.8" },
        "react-dom": { singleton: true, requiredVersion: "^19.2.8" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 5184,
    strictPort: true,
    cors: true,
    origin: "http://localhost:5184",
  },
  preview: {
    port: 5184,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
