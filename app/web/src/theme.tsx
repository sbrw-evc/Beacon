import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { animate } from 'motion/react'
import type { Theme } from './api'
import { reducedMotion, viewTransition, type Origin } from './fx'

const KEY = 'umbrella.theme'

type Ctx = { theme: Theme; setTheme: (t: Theme, persist?: boolean, origin?: Origin) => void }

const ThemeContext = createContext<Ctx>({ theme: 'light', setTheme: () => {} })

export function savedTheme(): Theme | null {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : null
  } catch {
    return null
  }
}

export function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t
  document.documentElement.style.colorScheme = t
}

function reveal(apply: () => void, origin?: Origin) {
  const root = document.documentElement
  const x = origin?.x ?? window.innerWidth - 40
  const y = origin?.y ?? 32
  root.style.setProperty('--reveal-x', `${x}px`)
  root.style.setProperty('--reveal-y', `${y}px`)
  root.style.setProperty('--reveal-r', '0px')
  root.classList.add('theme-reveal')
  const vt = viewTransition(apply)
  if (!vt) {
    root.classList.remove('theme-reveal')
    apply()
    animate(document.body, { opacity: [0.35, 1] }, { duration: 0.35, ease: 'easeOut' })
    return
  }
  const radius = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y))
  vt.ready
    .then(() => animate(0, radius, { duration: 0.6, ease: [0.22, 1, 0.36, 1], onUpdate: (v) => root.style.setProperty('--reveal-r', `${v}px`) }))
    .catch(() => undefined)
  vt.finished.finally(() => root.classList.remove('theme-reveal'))
}

export function ThemeProvider({ initial, children }: { initial: Theme; children: ReactNode }) {
  const [theme, set] = useState<Theme>(initial)
  const current = useRef(initial)
  applyTheme(theme)
  const setTheme = useCallback((t: Theme, persist = true, origin?: Origin) => {
    const apply = () => {
      current.current = t
      flushSync(() => set(t))
      applyTheme(t)
    }
    if (t === current.current || reducedMotion()) apply()
    else reveal(apply, origin)
    if (!persist) return
    try {
      localStorage.setItem(KEY, t)
    } catch {
      return
    }
  }, [])
  return <ThemeContext.Provider value={{ theme, setTheme }}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  return useContext(ThemeContext)
}
