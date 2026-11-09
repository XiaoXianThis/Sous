/** Keep Markdown links independent of the deployment prefix; preserve queries and anchors. */
export default function rehypeBaseLinks({ base = '/' } = {}) {
  const prefix = base.replace(/\/+$/, '');
  return function transform(tree) {
    function visit(node) {
      const href = node.properties?.href;
      if (typeof href === 'string' && href.startsWith('/') && !href.startsWith('//')) {
        const url = new URL(href, 'https://site.invalid');
        const pathname = url.pathname;
        const route = pathname.endsWith('/') || /\.[^/]+$/.test(pathname) ? pathname : `${pathname}/`;
        node.properties.href = `${prefix}${route}${url.search}${url.hash}`;
      }
      for (const child of node.children || []) visit(child);
    }
    visit(tree);
  };
}
