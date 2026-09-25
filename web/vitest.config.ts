import { defineConfig } from "vitest/config";

// Deliberately standalone rather than mergeConfig(viteConfig, ...): the only
// tests here are pure unit tests of the runtime endpoint resolver, which need
// neither the React plugin nor the Module Federation plugin. Importing
// vite.config.ts would pull both in for no benefit, and the fleet's other
// remotes have already hit "Cannot merge config in form of callback" doing it.
//
// No jsdom either -- src/config.ts guards its window access, so the resolver
// is exercised in vitest's default node environment.
export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
  },
});
