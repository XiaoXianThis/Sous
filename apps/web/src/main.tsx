import { MotionConfig } from 'motion/react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './app.css'
import { TipLayer } from './components/ui/tooltip'
import { installTouch } from './lib/ios'
import { applyTheme } from './lib/theme'

applyTheme()
installTouch()

// Motion (motion.dev) drives the topic folds and the project dialog. With the
// system's reduce-motion setting, its transforms (moves, scales) jump instead
// of animating; components animating anything else check it themselves.
createRoot(document.getElementById('root')!).render(
  <MotionConfig reducedMotion="user">
    <App />
    <TipLayer />
  </MotionConfig>,
)

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js').catch((e) => console.warn('service worker', e))
}
