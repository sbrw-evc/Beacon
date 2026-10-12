import { useEffect, useRef, useState } from 'react'
import { Bell, Trash2, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useLocale, useT } from '../i18n'
import { clearHistory, markAllRead, MAX_HISTORY, noteIcons, useNotes, type Note } from '../notify'
import { useRouter } from '../router'
import { Button, formatDate } from '../ui'
import { useSession } from './session'
import { topbarStrings } from './topbarStrings'
import { ask } from '../confirm'

// NotificationCenter is the bell of the top bar: it counts unread notifications and opens the
// history of them in a panel on the right.
export function NotificationCenter() {
  const t = useT(topbarStrings)
  const { history } = useNotes()
  const { path } = useRouter()
  const [open, setOpen] = useState(false)
  // fresh: what was unread when the panel opened stays marked while it is open.
  const [fresh, setFresh] = useState<Set<string>>(new Set())
  const trigger = useRef<HTMLButtonElement>(null)
  const unread = history.filter((n) => !n.read).length

  useEffect(() => setOpen(false), [path])

  const show = () => {
    setFresh(new Set(history.filter((n) => !n.read).map((n) => n.id)))
    setOpen(true)
  }
  const hide = () => {
    setOpen(false)
    trigger.current?.focus()
  }

  // Notifications that come while the panel is open are read there.
  useEffect(() => {
    if (open) markAllRead()
  }, [open, history])

  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="icon-btn nc-btn"
        aria-label={unread ? `${t('nc.open')}: ${t('nc.unread', { n: unread })}` : t('nc.open')}
        title={t('nc.open')}
        aria-expanded={open}
        aria-controls="nc-panel"
        onClick={() => (open ? hide() : show())}
      >
        <Bell size={18} />
        <AnimatePresence>
          {unread > 0 && (
            <motion.span
              key="badge"
              className="nc-badge"
              initial={{ scale: 0 }}
              animate={{ scale: 1 }}
              exit={{ scale: 0 }}
              transition={{ type: 'spring', stiffness: 500, damping: 28 }}
            >
              {unread > 99 ? '99+' : unread}
            </motion.span>
          )}
        </AnimatePresence>
      </button>
      <AnimatePresence>{open && <Panel fresh={fresh} onClose={hide} />}</AnimatePresence>
    </>
  )
}

function Panel({ fresh, onClose }: { fresh: Set<string>; onClose: () => void }) {
  const t = useT(topbarStrings)
  const { history } = useNotes()
  const close = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    close.current?.focus()
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const today = new Date().toDateString()
  const groups: [string, Note[]][] = [
    [t('nc.today'), history.filter((n) => new Date(n.at).toDateString() === today)],
    [t('nc.earlier'), history.filter((n) => new Date(n.at).toDateString() !== today)],
  ]

  return (
    <>
      <motion.div className="nc-backdrop" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onMouseDown={onClose} />
      <motion.aside
        id="nc-panel"
        className="nc-panel"
        role="dialog"
        aria-modal="true"
        aria-label={t('nc.title')}
        initial={{ x: '100%' }}
        animate={{ x: 0 }}
        exit={{ x: '100%' }}
        transition={{ type: 'spring', stiffness: 380, damping: 38 }}
      >
        <header className="nc-head">
          <h2>{t('nc.title')}</h2>
          <div className="row">
            {history.length > 0 && (
              <Button variant="ghost" onClick={async () => (await ask({ text: t('nc.clear.confirm'), danger: true })) && clearHistory()}>
                <Trash2 size={15} />
                {t('nc.clear')}
              </Button>
            )}
            <button ref={close} type="button" className="icon-btn" aria-label={t('nc.close')} title={t('nc.close')} onClick={onClose}>
              <X size={18} />
            </button>
          </div>
        </header>
        <div className="nc-list">
          {history.length === 0 ? (
            <p className="muted nc-empty">{t('nc.empty')}</p>
          ) : (
            groups.map(
              ([label, notes]) =>
                notes.length > 0 && (
                  <section key={label}>
                    <h3 className="nc-group">{label}</h3>
                    <ul>
                      {notes.map((n) => (
                        <Item key={n.id} note={n} fresh={fresh.has(n.id)} onOpen={onClose} />
                      ))}
                    </ul>
                  </section>
                ),
            )
          )}
          {history.length > 0 && <p className="hint nc-kept">{t('nc.kept', { n: MAX_HISTORY })}</p>}
        </div>
      </motion.aside>
    </>
  )
}

function Item({ note, fresh, onOpen }: { note: Note; fresh: boolean; onOpen: () => void }) {
  const t = useT(topbarStrings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  const { navigate } = useRouter()
  const Icon = noteIcons[note.kind]
  return (
    <li className={`nc-item nc-${note.kind}${fresh ? ' nc-fresh' : ''}`}>
      <Icon size={16} className="nc-icon" aria-hidden />
      <div className="nc-text">
        <div className="nc-title">{note.title}</div>
        {note.body && <div className="nc-body">{note.body}</div>}
        <div className="nc-meta">
          <time dateTime={new Date(note.at).toISOString()}>{formatDate(new Date(note.at).toISOString(), locale, timezone)}</time>
          {note.link && (
            <button
              type="button"
              className="link-btn"
              onClick={() => {
                onOpen()
                navigate(note.link!)
              }}
            >
              {t('nc.open.link')}
            </button>
          )}
        </div>
      </div>
    </li>
  )
}
