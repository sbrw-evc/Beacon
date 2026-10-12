import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { useT } from '../i18n'
import { MAX_SHOWN, notify, type NoteKind } from '../notify'
import { useRouter } from '../router'
import { useLiveReload } from './incidents/live'
import { SEVERITIES, SEVERITY_PRIORITY, SEVERITY_TONE, severityText, type Incident, type Page, type Severity } from './incidents/types'
import { useSession } from './session'
import { topbarStrings } from './topbarStrings'
import { publishCounts } from './mobile/lightsStore'

// The top bar keeps an eye on the active incidents of the user's scope: the lights count them by
// priority, and an incident that opens (or opens again) while the application is open pops up.

const SAFETY_MS = 30_000
const OFFLINE_MS = 15_000
// The newest active incidents are compared with the ones already known; new ones come first.
const WATCH = 50

const KIND: Record<Severity, NoteKind> = { critical: 'error', error: 'error', warning: 'warn', low: 'info', info: 'info' }

// compact: the phone layout shows one light, the highest priority that has incidents and how
// many incidents are active in all.
export function IncidentLights({ compact = false }: { compact?: boolean }) {
  const { can } = useSession()
  return can('incidents:view') ? <Lights compact={compact} /> : null
}

function Lights({ compact }: { compact: boolean }) {
  const t = useT(topbarStrings)
  const { navigate } = useRouter()
  const [counts, setCounts] = useState<Page['counts']['by_severity'] | null>(null)
  const [failed, setFailed] = useState(false)
  // seen: the incidents known so far, by ID, with the time they opened; null before the first load.
  const seen = useRef<Map<string, string> | null>(null)
  const running = useRef(false)
  const again = useRef(false)
  const tr = useRef(t)
  tr.current = t

  const announce = useCallback((fresh: Incident[]) => {
    const t = tr.current
    const rank = (a: Incident) => SEVERITIES.indexOf(a.severity)
    fresh.sort((a, b) => rank(a) - rank(b))
    if (fresh.length > MAX_SHOWN) {
      const top = fresh[0]
      notify({
        kind: KIND[top.severity] ?? 'info',
        title: t('inc.new.many', { n: fresh.length }),
        body: t('inc.new.many.text', { priority: severityText(t, top.severity), title: top.title }),
        link: '/incidents',
        key: `inc:${fresh.map((a) => `${a.id}@${a.opened_at}`).join(',')}`,
      })
      return
    }
    for (const a of fresh.reverse()) {
      notify({
        kind: KIND[a.severity] ?? 'info',
        title: t('inc.new', { priority: severityText(t, a.severity) }),
        body: a.ci_name ? `${a.title}\n${t('inc.new.ci', { ci: a.ci_name })}` : a.title,
        link: `/incidents?id=${encodeURIComponent(a.id)}`,
        key: `inc:${a.id}@${a.opened_at}`,
      })
    }
  }, [])

  const load = useCallback(async () => {
    if (running.current) {
      again.current = true
      return
    }
    running.current = true
    try {
      do {
        again.current = false
        try {
          const page = await api<Page>('GET', `/api/incidents?status=active&limit=${WATCH}`)
          setCounts(page.counts.by_severity)
          publishCounts(page.counts.by_severity)
          setFailed(false)
          const known = seen.current
          const fresh: Incident[] = []
          const next = known ?? new Map<string, string>()
          for (const a of page.alerts) {
            if (known && known.get(a.id) !== a.opened_at && a.status === 'open' && !a.suppressed) fresh.push(a)
            next.set(a.id, a.opened_at)
          }
          seen.current = next
          if (fresh.length > 0) announce(fresh)
        } catch {
          setFailed(true)
        }
      } while (again.current)
    } finally {
      running.current = false
    }
  }, [announce])

  const live = useLiveReload(load)
  useEffect(() => {
    void load()
  }, [load])
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') void load()
    }, live ? SAFETY_MS : OFFLINE_MS)
    return () => window.clearInterval(id)
  }, [live, load])

  useEffect(() => () => publishCounts(null), [])

  if (compact) {
    const top = SEVERITIES.find((s) => (counts?.[s] ?? 0) > 0)
    const total = SEVERITIES.reduce((n, s) => n + (counts?.[s] ?? 0), 0)
    const label = top ? t('lights.compact', { priority: severityText(t, top), n: total }) : t('lights.calm')
    return (
      <button
        type="button"
        className={`lights lights-compact${failed && !counts ? ' lights-off' : ''}`}
        aria-label={label}
        title={failed ? t('lights.offline') : label}
        onClick={() => navigate(top ? `/incidents?severity=${top}` : '/incidents')}
      >
        <span className={`light light-${top ? SEVERITY_TONE[top] : 'ok'}${top ? ' on' : ''}${top === 'critical' ? ' light-top' : ''}`}>
          <span className="light-dot" aria-hidden />
          {top && <span className="light-p">{SEVERITY_PRIORITY[top]}</span>}
          <span className="light-n">{counts ? total : '–'}</span>
        </span>
      </button>
    )
  }

  return (
    <div className={`lights${failed && !counts ? ' lights-off' : ''}`} role="group" aria-label={t('lights.label')} title={failed ? t('lights.offline') : undefined}>
      {SEVERITIES.map((s) => {
        const n = counts?.[s] ?? 0
        const label = t('lights.item', { priority: severityText(t, s), n })
        return (
          <button
            key={s}
            type="button"
            className={`light light-${SEVERITY_TONE[s]}${s === 'critical' ? ' light-top' : ''}${n > 0 ? ' on' : ''}`}
            aria-label={label}
            title={label}
            onClick={() => navigate(`/incidents?severity=${s}`)}
          >
            <span className="light-dot" aria-hidden />
            <span className="light-p">{SEVERITY_PRIORITY[s]}</span>
            <span className="light-n">{counts ? n : '–'}</span>
          </button>
        )
      })}
    </div>
  )
}
