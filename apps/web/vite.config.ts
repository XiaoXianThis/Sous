import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// In dev, the Hub runs on :8787 (wok dev core) and Vite proxies the API to it.
const hub = process.env.SOUS_HUB ?? 'http://127.0.0.1:8787'

export default defineConfig({
  plugins: [svelte()],
  // Tests use Svelte's client build, so $state and $effect behave as in the browser.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  server: {
    port: 5173,
    proxy: {
      '/api': hub,
      '/ws': { target: hub, ws: true },
      '/healthz': hub,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    include: ['src/**/*.test.ts'],
    // A DOM environment makes Vitest compile Svelte modules for the client.
    environment: 'happy-dom',
  },
})
