import { createRoot } from 'react-dom/client'
import App from './App'
import './app.css'
import { installTouch } from './lib/ios'
import { applyTheme } from './lib/theme'

applyTheme()
installTouch()

createRoot(document.getElementById('root')!).render(<App />)

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js').catch((e) => console.warn('service worker', e))
}
