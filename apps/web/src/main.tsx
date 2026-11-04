import { createRoot } from 'react-dom/client'
import App from './App'
import './app.css'

createRoot(document.getElementById('root')!).render(<App />)

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js').catch((e) => console.warn('service worker', e))
}
