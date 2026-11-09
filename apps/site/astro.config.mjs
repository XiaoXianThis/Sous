import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';
import rehypeBaseLinks from './scripts/rehype-base-links.mjs';

const base = '/Sous';

export default defineConfig({
  site: 'https://xiaoxianthis.github.io',
  base,
  trailingSlash: 'always',
  outDir: process.env.WOK_OUT || '../../build/site',
  integrations: [
    tailwind({
      applyBaseStyles: false,
    }),
  ],
  markdown: {
    rehypePlugins: [[rehypeBaseLinks, { base }]],
    shikiConfig: {
      themes: {
        light: 'github-light',
        dark: 'github-dark-default',
      },
    },
  },
});
