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

/**
 * The page is hosted on Vercel (decision 0030) and talks only to the relay.
 * vercel.json carries a strict CSP: scripts only from this origin, network
 * only to the relay named by VITE_SOUS_RELAY at build time.
 */
function vercelConfig(): Plugin {
  return {
    name: 'sous-vercel',
    apply: 'build',
    generateBundle() {
      const relay = (process.env.VITE_SOUS_RELAY ?? '').replace(/\/+$/, '')
      let connect = "'self'"
      if (relay) {
        const u = new URL(relay)
        const ws = (u.protocol === 'https:' ? 'wss://' : 'ws://') + u.host
        connect += ` ${u.origin} ${ws}`
      } else {
        this.warn('VITE_SOUS_RELAY is not set: the CSP lets the page reach any https/wss host. Set it for a release build.')
        connect += ' https: wss:'
      }
      const csp = [
        "default-src 'self'", "script-src 'self'", "style-src 'self' 'unsafe-inline'", "img-src 'self' data: blob:",
        "font-src 'self' data:", `connect-src ${connect}`, "worker-src 'self'", "manifest-src 'self'",
        "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'", "object-src 'none'",
      ].join('; ')
      const headers = [
        { key: 'Content-Security-Policy', value: csp },
        { key: 'Referrer-Policy', value: 'no-referrer' },
        { key: 'X-Content-Type-Options', value: 'nosniff' },
        { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=(), publickey-credentials-get=(self), publickey-credentials-create=(self)' },
        { key: 'Strict-Transport-Security', value: 'max-age=63072000; includeSubDomains' },
      ]
      const config = {
        headers: [
          { source: '/(.*)', headers },
          { source: '/sw.js', headers: [{ key: 'Cache-Control', value: 'no-cache' }] },
          { source: '/index.html', headers: [{ key: 'Cache-Control', value: 'no-cache' }] },
        ],
        cleanUrls: false,
        trailingSlash: false,
      }
      this.emitFile({ type: 'asset', fileName: 'vercel.json', source: JSON.stringify(config, null, 2) + '\n' })
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), buildInfo(), vercelConfig()],
  define: { __BUILD__: JSON.stringify(build) },
  // In dev the page talks to a local relay (VITE_SOUS_RELAY, or the address
  // typed at first setup); there is nothing to proxy.
  server: { port: 5173 },
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
