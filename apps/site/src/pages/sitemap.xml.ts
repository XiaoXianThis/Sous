import type { APIRoute } from 'astro';
import { getCollection } from 'astro:content';
import { SITE_URL, GROUP_RANK } from '../lib/docs-nav';

export const GET: APIRoute = async () => {
  const docs = await getCollection('docs');
  const sorted = [...docs].sort((a, b) => {
    const gDiff = GROUP_RANK[a.data.group] - GROUP_RANK[b.data.group];
    if (gDiff !== 0) return gDiff;
    return a.data.order - b.data.order;
  });

  const staticPages = [
    { path: '/', lastmod: '2026-11-09', priority: '1.0' },
    { path: '/docs', lastmod: '2026-11-09', priority: '0.9' },
    { path: '/changelog', lastmod: '2026-11-09', priority: '0.8' },
  ];

  const docPages = sorted.map((entry) => ({
    path: `/docs/${entry.id}`,
    lastmod: entry.data.updatedAt || '2026-11-09',
    priority: '0.8',
  }));

  const urls = [...staticPages, ...docPages];

  const xml = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls
  .map(
    (u) => `  <url>
    <loc>${SITE_URL}${u.path}</loc>
    <lastmod>${u.lastmod}</lastmod>
    <priority>${u.priority}</priority>
  </url>`
  )
  .join('\n')}
</urlset>
`;

  return new Response(xml, {
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
    },
  });
};
