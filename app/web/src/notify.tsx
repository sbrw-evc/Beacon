import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useT, type Dict } from './i18n'
import { useRouter } from './router'

// Notifications: the results of actions (a saved form, a checked connection, a failed sign-in) and
// new incidents pop up in the bottom right corner, at most MAX_SHOWN at a time, and are kept in
// the history of the signed-in user in this browser (localStorage), newest first.

export type NoteKind = 'ok' | 'error' | 'warn' | 'info'

export type Note = {
  id: string
  kind: NoteKind
  title: string
  body?: string
  // link: a page of the application the notification opens, e.g. an incident.
  link?: string
  at: number
  read?: boolean
}

export type NoteInput = Omit<Note, 'id' | 'at' | 'read'> & {
  // key: one notification per key, e.g. one per opened incident even with several tabs open.
  key?: string
}

export const MAX_SHOWN = 5
export const MAX_HISTORY = 200
// A notification that repeats the one shown just before is dropped: the same result rendered twice.
const REPEAT_MS = 1500
const LIFE_MS: Record<NoteKind, number> = { ok: 5000, info: 6000, warn: 8000, error: 10000 }
const KEY = 'umbrella.notifications.'

type State = { shown: Note[]; history: Note[] }

let state: State = { shown: [], history: [] }
let owner = ''
let seq = 0
const listeners = new Set<() => void>()

function emit(next: State) {
  state = next
  for (const l of listeners) l()
}

function load(user: string): Note[] {
  if (!user) return []
  try {
    const v = JSON.parse(localStorage.getItem(KEY + user) ?? '[]')
    return Array.isArray(v) ? (v as Note[]).filter((n) => n && typeof n.id === 'string' && typeof n.title === 'string') : []
  } catch {
    return []
  }
}

function save(history: Note[]) {
  if (!owner) return
  try {
    localStorage.setItem(KEY + owner, JSON.stringify(history))
  } catch {
    return
  }
}

if (typeof window !== 'undefined') {
  // Another tab of the same user changed the history.
  window.addEventListener('storage', (e) => {
    if (owner && e.key === KEY + owner) emit({ ...state, history: load(owner) })
  })
}

// setNotifyOwner switches the history to the signed-in user; '' (signed out) keeps none.
export function setNotifyOwner(user: string) {
  if (user === owner) return
  owner = user
  emit({ shown: user ? state.shown : [], history: load(user) })
}

export function notify(input: NoteInput) {
  const { key, ...rest } = input
  const now = Date.now()
  const last = state.shown[state.shown.length - 1]
  if (last && now - last.at < REPEAT_MS && last.kind === rest.kind && last.title === rest.title && last.body === rest.body) return
  const id = key ?? `n${now.toString(36)}-${(++seq).toString(36)}`
  if (key) {
    // Another tab may have shown it already.
    const history = owner ? load(owner) : state.history
    if (history.some((n) => n.id === key) || state.shown.some((n) => n.id === key)) return
  }
  const note: Note = { ...rest, id, at: now }
  const history = owner ? [note, ...load(owner)].slice(0, MAX_HISTORY) : state.history
  if (owner) save(history)
  emit({ shown: [...state.shown, note].slice(-MAX_SHOWN), history })
}

export function dismiss(id: string) {
  if (!state.shown.some((n) => n.id === id)) return
  emit({ ...state, shown: state.shown.filter((n) => n.id !== id) })
}

export function markAllRead() {
  if (!state.history.some((n) => !n.read)) return
  const history = state.history.map((n) => (n.read ? n : { ...n, read: true }))
  save(history)
  emit({ ...state, history })
}

export function clearHistory() {
  save([])
  emit({ ...state, history: [] })
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useNotes() {
  return useSyncExternalStore(subscribe, () => state)
}

export const noteIcons = { ok: CheckCircle2, error: XCircle, warn: AlertTriangle, info: Info }

export const notifyStrings: Dict = {
  en: {
    'nt.close': 'Close the notification',
    'nt.region': 'Notifications',
    'nt.open': 'Open',
  },
  ru: {
    'nt.close': 'Закрыть уведомление',
    'nt.region': 'Уведомления',
    'nt.open': 'Открыть',
  },
}

// Toaster shows the current notifications in the bottom right corner.
export function Toaster() {
  const { shown } = useNotes()
  const t = useT(notifyStrings)
  return (
    <section className="toaster" aria-label={t('nt.region')}>
      <AnimatePresence initial={false}>
        {shown.map((n) => (
          <Toast key={n.id} note={n} />
        ))}
      </AnimatePresence>
    </section>
  )
}

function Toast({ note }: { note: Note }) {
  const t = useT(notifyStrings)
  const { navigate } = useRouter()
  const [paused, setPaused] = useState(false)
  const left = useRef(LIFE_MS[note.kind])
  useEffect(() => {
    if (paused) return
    const started = Date.now()
    const id = window.setTimeout(() => dismiss(note.id), left.current)
    return () => {
      window.clearTimeout(id)
      left.current = Math.max(left.current - (Date.now() - started), 1500)
    }
  }, [paused, note.id])
  const Icon = noteIcons[note.kind]
  const open = note.link
    ? () => {
        dismiss(note.id)
        navigate(note.link!)
      }
    : undefined
  return (
    <motion.div
      layout
      className={`toast toast-${note.kind}${open ? ' toast-link' : ''}`}
      role={note.kind === 'error' ? 'alert' : 'status'}
      initial={{ opacity: 0, x: 40, scale: 0.96 }}
      animate={{ opacity: 1, x: 0, scale: 1 }}
      exit={{ opacity: 0, x: 40, transition: { duration: 0.18 } }}
      transition={{ type: 'spring', stiffness: 420, damping: 32 }}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
    >
      <Icon size={18} className="toast-icon" aria-hidden />
      <div className="toast-text">
        <div className="toast-title">{note.title}</div>
        {note.body && <div className="toast-body">{note.body}</div>}
        {open && (
          <button type="button" className="link-btn toast-open" onClick={open}>
            {t('nt.open')}
          </button>
        )}
      </div>
      <button type="button" className="icon-btn toast-close" aria-label={t('nt.close')} title={t('nt.close')} onClick={() => dismiss(note.id)}>
        <X size={16} />
      </button>
    </motion.div>
  )
}

const BLOCK = new Set(['DIV', 'P', 'LI', 'UL', 'OL', 'DL', 'DT', 'DD', 'TABLE', 'TR', 'SECTION', 'BR', 'H3', 'H4'])

// textOf reads the text of rendered content, a line per block.
function textOf(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? ''
  if (!(node instanceof HTMLElement)) return ''
  if (node.tagName === 'BUTTON' || node.tagName === 'INPUT' || node.tagName === 'A') return ''
  const inner = Array.from(node.childNodes).map(textOf).join('')
  if (node.tagName === 'DT') return `\n${inner}: `
  return BLOCK.has(node.tagName) ? `\n${inner}\n` : inner
}

function plain(el: HTMLElement | null) {
  if (!el) return undefined
  const lines = Array.from(el.childNodes)
    .map(textOf)
    .join('')
    .split('\n')
    .map((l) => l.replace(/\s+/g, ' ').trim())
    .filter(Boolean)
  return lines.length ? lines.join('\n') : undefined
}

const flashed = new WeakMap<object, string>()

// Flash turns a result that used to be shown in place into a notification: it notifies when it
// appears and whenever its text (or trigger, e.g. the result of a new check) changes, and shows
// nothing itself. Its children are taken as text.
export function Flash({ kind, title, link, trigger, children }: { kind: NoteKind; title: string; link?: string; trigger?: unknown; children?: ReactNode }) {
  const box = useRef<HTMLSpanElement>(null)
  const last = useRef<{ sig: string; trigger: unknown } | null>(null)
  useEffect(() => {
    const body = plain(box.current)
    const sig = `${kind}\n${title}\n${body ?? ''}`
    if (last.current && last.current.sig === sig && last.current.trigger === trigger) return
    last.current = { sig, trigger }
    // A result shown again (the form went back to the settings that were checked) is not news.
    if (trigger && typeof trigger === 'object') {
      if (flashed.get(trigger) === sig) return
      flashed.set(trigger, sig)
    }
    notify({ kind, title, body, link })
  })
  return children ? (
    <span hidden ref={box}>
      {children}
    </span>
  ) : null
}
