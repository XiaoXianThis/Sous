import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const buildDir = process.env.WOK_OUT || path.resolve(__dirname, '../../../build/site');

const requiredFiles = [
  'index.html',
  '404.html',
  'docs/index.html',
  'docs/getting-started/index.html',
  'docs/connect-machine/index.html',
  'docs/architecture-overview/index.html',
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

for (const file of htmlFiles) {
  const content = fs.readFileSync(file, 'utf8');
  const ids = new Set();
  for (const m of content.matchAll(/\bid="([^"]+)"/g)) {
    ids.add(m[1]);
  }
  pageIds.set(file, ids);
}

function resolveRouteToFile(routePath) {
  const clean = routePath.replace(/\/+$/, '') || '/';
  if (clean === '/') return path.join(buildDir, 'index.html');
  if (clean === '/404') return path.join(buildDir, '404.html');
  const candidateIndex = path.join(buildDir, clean.slice(1), 'index.html');
  if (fs.existsSync(candidateIndex)) return candidateIndex;
  const candidateDirect = path.join(buildDir, clean.slice(1));
  if (fs.existsSync(candidateDirect)) return candidateDirect;
  return null;
}

let checkedLinks = 0;
for (const file of htmlFiles) {
  const content = fs.readFileSync(file, 'utf8');
  const relFile = path.relative(buildDir, file);
  for (const m of content.matchAll(/\bhref="([^"]+)"/g)) {
    const href = m[1];
    checkedLinks++;
    if (href.startsWith('http://') || href.startsWith('https://')) {
      new URL(href);
      continue;
    }
    if (href.startsWith('#')) {
      const anchor = decodeURIComponent(href.slice(1));
      if (anchor && !pageIds.get(file)?.has(anchor)) {
        console.error(`Broken in-page anchor ${href} in ${relFile}`);
        process.exit(1);
      }
      continue;
    }
    if (href.startsWith('/')) {
      const [routePart, hashPart] = href.split('#');
      const targetFile = resolveRouteToFile(routePart);
      if (!targetFile) {
        console.error(`Dead internal link ${href} in ${relFile}`);
        process.exit(1);
      }
      if (hashPart && targetFile.endsWith('.html')) {
        const anchor = decodeURIComponent(hashPart);
        if (anchor && !pageIds.get(targetFile)?.has(anchor)) {
          console.error(`Broken cross-page anchor ${href} in ${relFile}`);
          process.exit(1);
        }
      }
    }
  }
}

console.log(`Verified ${htmlFiles.length} HTML pages and ${checkedLinks} links in ${buildDir}. All reachable.`);
