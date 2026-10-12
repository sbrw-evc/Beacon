import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
  type TextareaHTMLAttributes,
} from 'react'
import { AlertTriangle, CheckCircle2, Eye, EyeOff, FileCheck2, Info, Loader2, Minus, Moon, Plus, Sun, Upload, X, XCircle } from 'lucide-react'
import { AnimatePresence, motion, type HTMLMotionProps, type Transition } from 'motion/react'
import { useLocale, useT } from './i18n'
import { useTheme } from './theme'
import { originOf } from './fx'
import { Select } from './select'

export const spring: Transition = { type: 'spring', stiffness: 420, damping: 30 }

type ButtonProps = Omit<HTMLMotionProps<'button'>, 'children'> & { children?: ReactNode; variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-soft'; busy?: boolean }

export function Button({ variant = 'secondary', busy, disabled, children, className, ...rest }: ButtonProps) {
  return (
    <motion.button
      {...rest}
      className={`btn btn-${variant} ${className ?? ''}`}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      whileTap={disabled || busy ? undefined : { scale: 0.97 }}
      transition={spring}
    >
      {busy && <Loader2 className="spin" size={16} aria-hidden />}
      {children}
    </motion.button>
  )
}

export function Field({ label, hint, children, optional }: { label: string; hint?: ReactNode; children: (id: string) => ReactNode; optional?: string }) {
  const id = useId()
  return (
    <div className="field">
      <label htmlFor={id}>
        {label}
        {optional && <span className="optional"> ({optional})</span>}
      </label>
      {children(id)}
      {hint && <div className="hint">{hint}</div>}
    </div>
  )
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`input ${props.className ?? ''}`} />
}

export function Password(props: InputHTMLAttributes<HTMLInputElement>) {
  const [shown, setShown] = useState(false)
  const t = useT(labels)
  return (
    <div className="password">
      <input {...props} type={shown ? 'text' : 'password'} className="input" />
      <button type="button" className="icon-btn" onClick={() => setShown(!shown)} aria-label={shown ? t('ui.hide') : t('ui.show')}>
        {shown ? <EyeOff size={16} /> : <Eye size={16} />}
      </button>
    </div>
  )
}

export { Select }

// A file picker drawn as a drop zone: click to choose a file or drop one on it.
export function FileInput({ id, accept, onFile }: { id?: string; accept?: string; onFile: (f: File) => void }) {
  const t = useT(labels)
  const [name, setName] = useState('')
  const [over, setOver] = useState(false)
  const take = (f: File | undefined) => {
    if (!f) return
    setName(f.name)
    onFile(f)
  }
  return (
    <label
      className={`file-drop${over ? ' over' : ''}${name ? ' has-file' : ''}`}
      onDragOver={(e) => {
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        take(e.dataTransfer.files?.[0])
      }}
    >
      <input
        id={id}
        type="file"
        accept={accept}
        className="file-drop-input"
        onChange={(e) => {
          take(e.target.files?.[0])
          e.target.value = ''
        }}
      />
      <span className="file-drop-icon" aria-hidden>
        {name ? <FileCheck2 size={18} /> : <Upload size={18} />}
      </span>
      <span className="file-drop-text">
        <span className="file-drop-title">{name || t('ui.file.choose')}</span>
        <span className="hint">{t(name ? 'ui.file.replace' : 'ui.file.drop')}</span>
      </span>
    </label>
  )
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className="input textarea" />
}

export function Switch({
  checked,
  onChange,
  label,
  hint,
  aside,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  label: string
  hint?: string
  aside?: ReactNode
}) {
  const id = useId()
  return (
    <div className="switch-row">
      <button id={id} type="button" role="switch" aria-checked={checked} className={`switch ${checked ? 'on' : ''}`} onClick={() => onChange(!checked)}>
        <span />
      </button>
      <label htmlFor={id}>
        <span>{label}</span>
        {hint && <span className="hint">{hint}</span>}
      </label>
      <AnimatePresence initial={false}>
        {aside && (
          <motion.div
            className="switch-aside"
            initial={{ opacity: 0, x: 8 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: 8 }}
            transition={{ duration: 0.16 }}
          >
            {aside}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

export function Stepper({
  value,
  onChange,
  min,
  max,
  label,
  id,
  suffix,
}: {
  value: number
  onChange: (v: number) => void
  min: number
  max: number
  label?: string
  id?: string
  suffix?: string
}) {
  const t = useT()
  const [draft, setDraft] = useState(String(value))
  useEffect(() => setDraft(String(value)), [value])
  const clamp = (v: number) => Math.min(max, Math.max(min, Math.trunc(v)))
  const commit = (text: string) => {
    const n = Number(text)
    const next = text.trim() === '' || Number.isNaN(n) ? value : clamp(n)
    setDraft(String(next))
    if (next !== value) onChange(next)
  }
  const step = (d: number) => onChange(clamp(value + d))
  return (
    <div className="stepper" role="group" aria-label={label}>
      <button type="button" className="stepper-btn" onClick={() => step(-1)} disabled={value <= min} aria-label={t('num.dec')} tabIndex={-1}>
        <Minus size={14} />
      </button>
      <input
        id={id}
        className="stepper-input"
        inputMode="numeric"
        aria-label={label}
        value={draft}
        size={Math.max(String(max).length, 2)}
        onChange={(e) => {
          const text = e.target.value.replace(/[^0-9]/g, '')
          setDraft(text)
          const n = Number(text)
          if (text !== '' && n >= min && n <= max) onChange(n)
        }}
        onBlur={(e) => commit(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowUp') {
            e.preventDefault()
            step(1)
          } else if (e.key === 'ArrowDown') {
            e.preventDefault()
            step(-1)
          } else if (e.key === 'Enter') commit(draft)
        }}
      />
      {suffix && <span className="stepper-suffix">{suffix}</span>}
      <button type="button" className="stepper-btn" onClick={() => step(1)} disabled={value >= max} aria-label={t('num.inc')} tabIndex={-1}>
        <Plus size={14} />
      </button>
    </div>
  )
}

export function SettingRow({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="setting-row">
      <div className="setting-text">
        <span>{label}</span>
        {hint && <span className="hint">{hint}</span>}
      </div>
      <div className="setting-control">{children}</div>
    </div>
  )
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: { value: T; label: ReactNode }[]
  onChange: (v: T) => void
  label: string
}) {
  const group = useId()
  return (
    <div className="segmented" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          className={value === o.value ? 'active' : ''}
          onClick={() => onChange(o.value)}
        >
          {value === o.value && <motion.span layoutId={`seg-${group}`} className="seg-pill" transition={spring} />}
          <span className="seg-label">{o.label}</span>
        </button>
      ))}
    </div>
  )
}

const icons = { ok: CheckCircle2, error: XCircle, warn: AlertTriangle, info: Info }

export function Banner({ kind, title, children }: { kind: keyof typeof icons; title: ReactNode; children?: ReactNode }) {
  const Icon = icons[kind]
  return (
    <motion.div
      className={`banner banner-${kind}`}
      role={kind === 'error' ? 'alert' : 'status'}
      initial={{ opacity: 0, y: 8 }}
      animate={kind === 'error' ? { opacity: 1, y: 0, x: [0, -6, 5, -3, 0] } : { opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: [0.22, 1, 0.36, 1] }}
    >
      <Icon size={18} aria-hidden />
      <div>
        <div className="banner-title">{title}</div>
        {children && <div className="banner-body">{children}</div>}
      </div>
    </motion.div>
  )
}

export function Rows({ rows, align = 'start' }: { rows: [ReactNode, ReactNode][]; align?: 'start' | 'end' }) {
  return (
    <dl className={`rows ${align === 'end' ? 'rows-end' : ''}`}>
      {rows.map(([k, v], i) => (
        <div key={i}>
          <dt>{k}</dt>
          <dd>{v === '' || v === undefined || v === null ? '—' : v}</dd>
        </div>
      ))}
    </dl>
  )
}

export function Brand({ subtitle }: { subtitle?: string }) {
  const t = useT()
  return (
    <div className="brand">
      <img src="/logo.svg" alt="" width={32} height={32} />
      <div>
        <div className="brand-name">{t('app.name')}</div>
        {subtitle && <div className="brand-sub">{subtitle}</div>}
      </div>
    </div>
  )
}

export function Preferences({ persist = true }: { persist?: boolean }) {
  const { theme, setTheme } = useTheme()
  const { locale, setLocale } = useLocale()
  const t = useT()
  return (
    <div className="prefs">
      <Segmented
        label={t('lang.toggle')}
        value={locale}
        onChange={(l) => setLocale(l, persist)}
        options={[
          { value: 'en', label: 'EN' },
          { value: 'ru', label: 'RU' },
        ]}
      />
      <button
        type="button"
        className="icon-btn"
        aria-label={t('theme.toggle')}
        title={t('theme.toggle')}
        onClick={(e) => setTheme(theme === 'dark' ? 'light' : 'dark', persist, originOf(e))}
      >
        <AnimatePresence mode="wait" initial={false}>
          <motion.span
            key={theme}
            className="pop-icon"
            initial={{ rotate: -90, scale: 0.4, opacity: 0 }}
            animate={{ rotate: 0, scale: 1, opacity: 1 }}
            exit={{ rotate: 90, scale: 0.4, opacity: 0 }}
            transition={{ duration: 0.22 }}
          >
            {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
          </motion.span>
        </AnimatePresence>
      </button>
    </div>
  )
}

export function formatDate(v: string | undefined | null, locale: string, timeZone?: string) {
  if (!v) return ''
  const d = new Date(v)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return ''
  try {
    return d.toLocaleString(locale === 'ru' ? 'ru-RU' : 'en-GB', timeZone ? { timeZone } : undefined)
  } catch {
    return d.toLocaleString(locale === 'ru' ? 'ru-RU' : 'en-GB')
  }
}

export function browserTimezone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

function offsetMinutes(zone: string, at: Date) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
  })
    .formatToParts(at)
    .reduce<Record<string, number>>((acc, p) => (p.type === 'literal' ? acc : { ...acc, [p.type]: Number(p.value) }), {})
  const asUTC = Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute)
  return Math.round((asUTC - Math.floor(at.getTime() / 60000) * 60000) / 60000)
}

function offsetLabel(min: number) {
  const sign = min < 0 ? '−' : '+'
  const a = Math.abs(min)
  return `UTC${sign}${String(Math.floor(a / 60)).padStart(2, '0')}:${String(a % 60).padStart(2, '0')}`
}

export type Zone = { id: string; offset: number; label: string }

let zoneCache: Zone[] | null = null

export function timezones(): Zone[] {
  if (zoneCache) return zoneCache
  const now = new Date()
  let ids: string[] = []
  try {
    ids = Intl.supportedValuesOf('timeZone')
  } catch {
    ids = []
  }
  if (!ids.includes('UTC')) ids = ['UTC', ...ids]
  zoneCache = ids
    .map((id) => {
      const offset = offsetMinutes(id, now)
      return { id, offset, label: `(${offsetLabel(offset)}) ${id.replaceAll('_', ' ')}` }
    })
    .sort((a, b) => a.offset - b.offset || a.id.localeCompare(b.id))
  return zoneCache
}

export function zoneLabel(id: string) {
  return timezones().find((z) => z.id === id)?.label ?? id
}

export function TimezoneSelect({ value, onChange, id, defaultLabel }: { value: string; onChange: (v: string) => void; id?: string; defaultLabel?: string }) {
  const zones = useMemo(timezones, [])
  const known = value === '' || zones.some((z) => z.id === value)
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
      {defaultLabel !== undefined && <option value="">{defaultLabel}</option>}
      {!known && <option value={value}>{value}</option>}
      {zones.map((z) => (
        <option key={z.id} value={z.id}>
          {z.label}
        </option>
      ))}
    </Select>
  )
}

export function Modal({
  open,
  title,
  onClose,
  children,
  footer,
}: {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
}) {
  const t = useT(labels)
  const box = useRef<HTMLDivElement>(null)
  const titleId = useId()
  // The latest onClose, so that a parent passing a new function on every render (a page that
  // re-renders on a timer) does not re-run the effect: it would move the focus to the first
  // field again and scroll the dialog back to the top.
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    if (!open) return
    const prev = document.activeElement as HTMLElement | null
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close.current()
    window.addEventListener('keydown', onKey)
    requestAnimationFrame(() => box.current?.querySelector<HTMLElement>('input, select, textarea, button')?.focus())
    return () => {
      window.removeEventListener('keydown', onKey)
      prev?.focus()
    }
  }, [open])
  return (
    <AnimatePresence>
      {open && (
        <motion.div
          className="modal-backdrop"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onMouseDown={(e) => e.target === e.currentTarget && onClose()}
        >
          <motion.div
            ref={box}
            className="card modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby={titleId}
            initial={{ opacity: 0, y: 16, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.98 }}
            transition={spring}
          >
            <header className="modal-head">
              <h2 id={titleId}>{title}</h2>
              <button type="button" className="icon-btn" onClick={onClose} aria-label={t('ui.close')}>
                <X size={18} />
              </button>
            </header>
            <div className="stack">{children}</div>
            {footer && <div className="modal-foot">{footer}</div>}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}

const labels = {
  en: {
    'ui.show': 'Show',
    'ui.hide': 'Hide',
    'ui.close': 'Close',
    'ui.file.choose': 'Choose a file',
    'ui.file.drop': 'or drop it here',
    'ui.file.replace': 'Click or drop another file to replace it',
  },
  ru: {
    'ui.show': 'Показать',
    'ui.hide': 'Скрыть',
    'ui.close': 'Закрыть',
    'ui.file.choose': 'Выберите файл',
    'ui.file.drop': 'или перетащите его сюда',
    'ui.file.replace': 'Нажмите или перетащите другой файл, чтобы заменить',
  },
}
