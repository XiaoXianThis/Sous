// Apply the saved theme before the app loads (src/lib/theme.ts keeps it up to date).
// A file rather than an inline script: the Hub's Content-Security-Policy forbids inline scripts.
try {
  var t = localStorage.getItem('sous.theme')
  if (t !== 'light' && t !== 'dark') t = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  document.documentElement.dataset.theme = t
} catch (e) {}
