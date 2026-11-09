/** Prefix a site-relative path with Astro's configured base, with one joining slash. */
export function withBase(path = '/'): string {
  const base = import.meta.env.BASE_URL.replace(/\/+$/, '');
  return `${base}/${path.replace(/^\/+/, '')}`;
}
