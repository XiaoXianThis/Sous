// Apply the saved theme before the app loads (src/lib/theme.ts keeps it up to date).
// A file rather than an inline script: the Hub's Content-Security-Policy forbids inline scripts.
try {
  var t = localStorage.getItem('sous.theme')
  if (t !== 'light' && t !== 'dark') t = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  document.documentElement.dataset.theme = t
  var c = document.querySelector('meta[name="theme-color"]')
  if (c) c.setAttribute('content', t === 'dark' ? '#141414' : '#f3f3f3')
  // A home-screen app reads this at launch: white status-bar text over the dark theme.
  var s = document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]')
  if (s) s.setAttribute('content', t === 'dark' ? 'black-translucent' : 'default')
} catch (e) {}
