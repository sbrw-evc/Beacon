import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Check, ChevronDown, ChevronRight, Languages, LogOut, Moon, Sun, UserRound } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import type { Locale, Theme } from '../api'
import { Avatar } from '../Avatar'
import { useLocale, useT } from '../i18n'
import { useRouter } from '../router'
import { useTheme } from '../theme'
import { spring } from '../ui'
import { PROFILE_PATH } from './pages'
import { roleLabel } from './types'
import { useSession } from './session'
import { strings } from './strings'

export function UserMenu({ onSignOut }: { onSignOut: () => void }) {
  const t = useT(strings)
  const { user } = useSession()
  const { path, navigate } = useRouter()
  const { theme, setTheme } = useTheme()
  const { locale, setLocale } = useLocale()
  const [open, setOpen] = useState(false)
  const [section, setSection] = useState<'language' | 'theme' | null>(null)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const id = useId()

  useEffect(() => setOpen(false), [path])

  useEffect(() => {
    if (!open) setSection(null)
  }, [open])

  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        trigger.current?.focus()
      }
    }
    document.addEventListener('pointerdown', onDown)
    document.addEventListener('keydown', onKey)
    requestAnimationFrame(() => menu.current?.querySelector<HTMLElement>('button, a')?.focus())
    return () => {
      document.removeEventListener('pointerdown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const origin = () => {
    const r = trigger.current?.getBoundingClientRect()
    return r ? { x: r.left + r.width / 2, y: r.top + r.height / 2 } : undefined
  }

  const subtitle = user.title || roleLabel(t, user.role, user.role_name)

  return (
    <div className="user-menu" ref={root}>
      <button
        ref={trigger}
        type="button"
        className="user-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={id}
        aria-label={t('menu.open')}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="who">
          <strong>{user.name}</strong>
          <small>{subtitle}</small>
        </span>
        <Avatar user={user} size={32} />
        <motion.span className="pop-icon chevron" animate={{ rotate: open ? 180 : 0 }} transition={spring}>
          <ChevronDown size={16} />
        </motion.span>
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            ref={menu}
            id={id}
            role="menu"
            className="card user-dropdown"
            initial={{ opacity: 0, y: -8, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -6, scale: 0.98 }}
            transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
            style={{ transformOrigin: 'top right' }}
          >
            <div className="menu-head">
              <Avatar user={user} size={44} />
              <div>
                <strong>{user.name}</strong>
                <small>{[subtitle, user.department].filter(Boolean).join(' · ')}</small>
                {user.email && <small className="menu-email">{user.email}</small>}
              </div>
            </div>
            <button type="button" role="menuitem" className="menu-item" onClick={() => navigate(PROFILE_PATH)}>
              <UserRound size={18} />
              {t('nav.profile')}
            </button>
            <Accordion
              icon={<Languages size={18} />}
              label={t('menu.language')}
              value={t(`lang.${locale}`)}
              open={section === 'language'}
              onToggle={() => setSection(section === 'language' ? null : 'language')}
              options={(['en', 'ru'] as Locale[]).map((l) => ({ key: l, label: t(`lang.${l}`), selected: l === locale, select: () => setLocale(l) }))}
            />
            <Accordion
              icon={theme === 'dark' ? <Moon size={18} /> : <Sun size={18} />}
              label={t('menu.theme')}
              value={t(`theme.${theme}`)}
              open={section === 'theme'}
              onToggle={() => setSection(section === 'theme' ? null : 'theme')}
              options={(['light', 'dark'] as Theme[]).map((v) => ({
                key: v,
                label: t(`theme.${v}`),
                icon: v === 'dark' ? <Moon size={16} /> : <Sun size={16} />,
                selected: v === theme,
                select: () => setTheme(v, true, origin()),
              }))}
            />
            <div className="menu-sep" />
            <button type="button" role="menuitem" className="menu-item danger" onClick={onSignOut}>
              <LogOut size={18} />
              {t('signout')}
            </button>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

type Option = { key: string; label: string; icon?: ReactNode; selected: boolean; select: () => void }

function Accordion({
  icon,
  label,
  value,
  open,
  onToggle,
  options,
}: {
  icon: ReactNode
  label: string
  value: string
  open: boolean
  onToggle: () => void
  options: Option[]
}) {
  const panel = useId()
  return (
    <div className={`menu-accordion ${open ? 'open' : ''}`}>
      <button type="button" className="menu-item" aria-expanded={open} aria-controls={panel} onClick={onToggle}>
        {icon}
        <span className="menu-label">{label}</span>
        <span className="menu-value">{value}</span>
        <motion.span className="pop-icon menu-chevron" animate={{ rotate: open ? 90 : 0 }} transition={spring}>
          <ChevronRight size={16} />
        </motion.span>
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            id={panel}
            role="group"
            aria-label={label}
            className="menu-options"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
          >
            {options.map((o) => (
              <button
                key={o.key}
                type="button"
                role="menuitemradio"
                aria-checked={o.selected}
                className={`menu-option ${o.selected ? 'selected' : ''}`}
                onClick={o.select}
              >
                {o.icon}
                <span className="menu-label">{o.label}</span>
                {o.selected && (
                  <motion.span
                    className="pop-icon"
                    initial={{ scale: 0.3 }}
                    animate={{ scale: 1 }}
                    transition={{ type: 'spring', stiffness: 520, damping: 18 }}
                  >
                    <Check size={16} />
                  </motion.span>
                )}
              </button>
            ))}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}
