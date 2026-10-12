import { useEffect, useId, useRef, useSyncExternalStore } from 'react'
import { AlertTriangle, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useT, type Dict } from './i18n'
import { Button, spring } from './ui'

// ask shows a confirmation dialog of the application (instead of the browser's confirm) and
// resolves with the answer. danger: the action deletes something, its button is red.

export type Ask = { text: string; title?: string; ok?: string; danger?: boolean }

type Pending = Ask & { resolve: (ok: boolean) => void }

let pending: Pending | null = null
const listeners = new Set<() => void>()

function emit(next: Pending | null) {
  pending = next
  for (const l of listeners) l()
}

export function ask(q: Ask): Promise<boolean> {
  pending?.resolve(false)
  return new Promise((resolve) => emit({ ...q, resolve }))
}

function answer(ok: boolean) {
  const p = pending
  if (!p) return
  emit(null)
  p.resolve(ok)
}

const strings: Dict = {
  en: { 'cf.title': 'Confirm the action', 'cf.ok': 'Confirm', 'cf.delete': 'Delete', 'cf.cancel': 'Cancel', 'cf.close': 'Close' },
  ru: { 'cf.title': 'Подтвердите действие', 'cf.ok': 'Подтвердить', 'cf.delete': 'Удалить', 'cf.cancel': 'Отмена', 'cf.close': 'Закрыть' },
}

// ConfirmHost renders the dialog ask opens; it is mounted once, above the pages and their dialogs.
export function ConfirmHost() {
  const q = useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => pending,
  )
  return <AnimatePresence>{q && <Dialog key="confirm" q={q} />}</AnimatePresence>
}

function Dialog({ q }: { q: Ask }) {
  const t = useT(strings)
  const titleId = useId()
  const ok = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    // Captured first, so Escape closes only this dialog and not the one under it.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopImmediatePropagation()
      answer(false)
    }
    window.addEventListener('keydown', onKey, true)
    requestAnimationFrame(() => ok.current?.focus())
    return () => {
      window.removeEventListener('keydown', onKey, true)
      prev?.focus()
    }
  }, [])
  return (
    <motion.div
      className="modal-backdrop confirm-backdrop"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      onMouseDown={(e) => e.target === e.currentTarget && answer(false)}
    >
      <motion.div
        className="card modal confirm"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        initial={{ opacity: 0, y: 16, scale: 0.97 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 8, scale: 0.98 }}
        transition={spring}
      >
        <header className="modal-head">
          <h2 id={titleId}>{q.title ?? t('cf.title')}</h2>
          <button type="button" className="icon-btn" onClick={() => answer(false)} aria-label={t('cf.close')}>
            <X size={18} />
          </button>
        </header>
        <div className={`confirm-text${q.danger ? ' confirm-danger' : ''}`}>
          {q.danger && <AlertTriangle size={20} aria-hidden />}
          <p>{q.text}</p>
        </div>
        <div className="modal-foot">
          <Button onClick={() => answer(false)}>{t('cf.cancel')}</Button>
          <Button ref={ok} variant={q.danger ? 'danger' : 'primary'} onClick={() => answer(true)}>
            {q.ok ?? t(q.danger ? 'cf.delete' : 'cf.ok')}
          </Button>
        </div>
      </motion.div>
    </motion.div>
  )
}
