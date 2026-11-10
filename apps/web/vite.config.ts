import { execFileSync } from 'node:child_process'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import type { Plugin } from 'vite'
import { defineConfig } from 'vitest/config'

/** The commit the page is built from, so the Hub can tell a stale build
 *  (end-to-end review 4.23). Empty outside a git checkout. */
function gitInfo(): { commit: string; dirty: boolean } {
  try {
    const commit = execFileSync('git', ['rev-parse', '--short=12', 'HEAD'], { encoding: 'utf8' }).trim()
    const dirty = execFileSync('git', ['status', '--porcelain', '--', '.'], { encoding: 'utf8' }).trim() !== ''
    return { commit, dirty }
  } catch {
    return { commit: '', dirty: false }
  }
}

const build = { ...gitInfo(), built_at: new Date().toISOString() }

/** Writes build-info.json next to index.html. */
function buildInfo(): Plugin {
  return {
    name: 'sous-build-info',
    apply: 'build',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: 'build-info.json', source: JSON.stringify(build) + '\n' })
    },
  }
}

// In dev, the Hub runs on :8787 (wok dev core) and Vite proxies the API to it.
const hub = process.env.SOUS_HUB ?? 'http://127.0.0.1:8787'

export default defineConfig({
  plugins: [react(), tailwindcss(), buildInfo()],
  define: { __BUILD__: JSON.stringify(build) },
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
    rollupOptions: {
      output: {
        // Libraries change less often than the app: separate chunks stay
        // cached across releases (end-to-end review 4.24).
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (/[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return 'react'
          // Icons, Radix (popovers, menus, dialogs) and the class helpers of components/ui.
          if (/[\\/](@hugeicons|@radix-ui|clsx|tailwind-merge)[\\/]/.test(id)) return 'ui'
          // Motion (motion.dev; `motion` re-exports framer-motion, motion-dom, motion-utils).
          if (/[\\/](motion|framer-motion|motion-dom|motion-utils)[\\/]/.test(id)) return 'motion'
          if (/[\\/](react-markdown|remark-|rehype-|micromark|mdast-|hast-|unified|unist-|vfile|property-information|decode-named|character-|space-separated|comma-separated|devlop|bail|trough|zwitch|ccount|longest-streak|markdown-table|html-url|estree|trim-lines|is-plain|inline-style|style-to)/.test(id)) return 'markdown'
          return undefined
        },
      },
    },
  },
  test: {
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    environment: 'happy-dom',
  },
})
