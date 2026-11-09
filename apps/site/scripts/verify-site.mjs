import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import config from '../astro.config.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const buildDir = process.env.WOK_OUT || path.resolve(__dirname, '../../../build/site');
const base = `${(config.base || '/').replace(/\/+$/, '')}/`;
const siteUrl = new URL(base, config.site);

const requiredFiles = [
  'index.html',
  '404.html',
  'changelog/index.html',
  'docs/index.html',
  'docs/introduction/index.html',
  'docs/getting-started/index.html',
  'docs/first-task/index.html',
  'docs/core-concepts/index.html',
  'docs/presets-and-machines/index.html',
  'docs/approval-mechanism/index.html',
  'docs/connect-machine/index.html',
  'docs/workbench-and-presets/index.html',
  'docs/git-and-schedules/index.html',
  'docs/cli-and-config/index.html',
  'docs/api-and-events/index.html',
  'docs/development-guide/index.html',
  'docs/architecture-overview/index.html',
  'docs/faq-troubleshooting/index.html',
  'sitemap.xml',
  'robots.txt',
  'og-placeholder.svg',
  'favicon.svg',
];

for (const rel of requiredFiles) {
  const full = path.join(buildDir, rel);
  if (!fs.existsSync(full)) {
    console.error(`Missing built file: ${rel} (expected at ${full})`);
    process.exit(1);
  }
}

function collectHtmlFiles(dir, acc = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) collectHtmlFiles(p, acc);
    else if (entry.isFile() && entry.name.endsWith('.html')) acc.push(p);
  }
  return acc;
}

const htmlFiles = collectHtmlFiles(buildDir);
const pageIds = new Map();
const seenTitles = new Map();
const seenDescriptions = new Map();

function pageUrl(file) {
  // Astro renders its special 404.html output using the /404/ route pathname.
  const route = path.relative(buildDir, file).split(path.sep).join('/').replace(/index\.html$/, '').replace(/^404\.html$/, '404/');
  return new URL(route, siteUrl);
}

function fail(message) {
  console.error(message);
  process.exit(1);
}

for (const file of htmlFiles) {
  const content = fs.readFileSync(file, 'utf8');
  const relFile = path.relative(buildDir, file);
  const ids = new Set();
  for (const m of content.matchAll(/\bid="([^"]+)"/g)) {
    ids.add(m[1]);
  }
  pageIds.set(file, ids);

  // Verify unique <title> and <meta name="description">
  const titleMatch = content.match(/<title>([^<]+)<\/title>/);
  if (!titleMatch || !titleMatch[1].trim()) {
    console.error(`Missing or empty <title> in ${relFile}`);
    process.exit(1);
  }
  const title = titleMatch[1].trim();
  if (seenTitles.has(title)) {
    console.error(`Duplicate <title> "${title}" in ${relFile} and ${seenTitles.get(title)}`);
    process.exit(1);
  }
  seenTitles.set(title, relFile);

  const descMatch = content.match(/<meta\s+name="description"\s+content="([^"]+)"/i);
  if (!descMatch || !descMatch[1].trim()) {
    console.error(`Missing or empty <meta name="description"> in ${relFile}`);
    process.exit(1);
  }
  const desc = descMatch[1].trim();
  if (seenDescriptions.has(desc)) {
    console.error(`Duplicate meta description in ${relFile} and ${seenDescriptions.get(desc)}`);
    process.exit(1);
  }
  seenDescriptions.set(desc, relFile);

  const canonical = content.match(/<link\s+rel="canonical"\s+href="([^"]+)"/i)?.[1];
  const ogUrl = content.match(/<meta\s+property="og:url"\s+content="([^"]+)"/i)?.[1];
  const expected = pageUrl(file).href;
  if (canonical !== expected || ogUrl !== expected) {
    fail(`Incorrect canonical/og:url in ${relFile}: expected ${expected}, got ${canonical} / ${ogUrl}`);
  }
}

function resolveRouteToFile(routePath) {
  if (!routePath.startsWith(base)) return null;
  const clean = `/${decodeURIComponent(routePath.slice(base.length))}`.replace(/\/+$/, '') || '/';
  if (clean === '/') return path.join(buildDir, 'index.html');
  if (clean === '/404') return path.join(buildDir, '404.html');
  const candidateIndex = path.join(buildDir, clean.slice(1), 'index.html');
  if (fs.existsSync(candidateIndex) && fs.statSync(candidateIndex).isFile()) return candidateIndex;
  const candidateDirect = path.join(buildDir, clean.slice(1));
  if (fs.existsSync(candidateDirect) && fs.statSync(candidateDirect).isFile()) return candidateDirect;
  return null;
}

let checkedLinks = 0;
function verifyLink(href, file, context = path.relative(buildDir, file)) {
  checkedLinks++;
  const url = new URL(href.replaceAll('&amp;', '&'), pageUrl(file));
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== siteUrl.origin) return;
  if (!url.pathname.startsWith(base)) {
    fail(`Internal URL escapes base ${base}: ${href} in ${context}`);
  }
  const targetFile = resolveRouteToFile(url.pathname);
  if (!targetFile) fail(`Dead internal link ${href} in ${context}`);
  if (url.hash && targetFile.endsWith('.html')) {
    const anchor = decodeURIComponent(url.hash.slice(1));
    if (anchor && !pageIds.get(targetFile)?.has(anchor)) {
      fail(`Broken anchor ${href} in ${context}`);
    }
  }
}

for (const file of htmlFiles) {
  const content = fs.readFileSync(file, 'utf8');
  // Ignore JavaScript templates, whose href values are populated from the search index.
  const markup = content.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, '');
  for (const m of markup.matchAll(/\b(?:href|src)="([^"]+)"/g)) {
    verifyLink(m[1], file);
  }
  const searchIndex = content.match(/const searchIndex = (\[[\s\S]*?\]);/)?.[1];
  if (!searchIndex) fail(`Missing search index in ${path.relative(buildDir, file)}`);
  for (const item of JSON.parse(searchIndex)) {
    verifyLink(item.href, file);
  }
  for (const m of content.matchAll(/<meta\s+(?:property="og:image"|name="twitter:image")\s+content="([^"]+)"/g)) {
    verifyLink(m[1], file);
  }
}

const sitemap = fs.readFileSync(path.join(buildDir, 'sitemap.xml'), 'utf8');
const locations = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
const expectedPages = htmlFiles.filter((file) => path.basename(file) !== '404.html').map((file) => pageUrl(file).href);
if (locations.length !== expectedPages.length || expectedPages.some((url) => !locations.includes(url))) {
  fail(`Sitemap must list all ${expectedPages.length} public pages under ${siteUrl.href}`);
}
for (const url of locations) verifyLink(url, path.join(buildDir, 'index.html'), 'sitemap.xml');
const robots = fs.readFileSync(path.join(buildDir, 'robots.txt'), 'utf8');
if (!robots.split(/\r?\n/).includes(`Sitemap: ${new URL('sitemap.xml', siteUrl).href}`)) {
  fail('robots.txt Sitemap does not match the configured site + base');
}

console.log(
  `Verified ${htmlFiles.length} HTML pages (unique titles & descriptions, canonical & og:url) and ${checkedLinks} links/assets/search/sitemap URLs in ${buildDir}. Base ${base}: all reachable; anchors valid.`
);
