import { useEffect, useState } from 'react'
import { CheckCircle2, ChevronDown, ChevronRight, ChevronUp, Wand2 } from 'lucide-react'
import { onChange } from '../../api'
import { useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Link } from '../../router'
import { Button } from '../../ui'
import type { Onboarding, OnboardingStep } from '../onboarding/types'
import { strings } from './strings'
import '../onboarding/onboarding.css'
import './guide.css'

// GuideAction is a step done with a button of the page itself (a dialog, a tab); the page passes
// what each one does.
export type GuideAction = 'create' | 'connect' | 'source' | 'integrations'

export type GuideActions = Partial<Record<GuideAction, () => void>>

const collapsedKey = (page: string) => `umbrella.guide.${page}`

function savedCollapsed(page: string) {
  try {
    return window.localStorage.getItem(collapsedKey(page)) === '1'
  } catch {
    // no storage: expanded
    return false
  }
}

// useGuide reads the guide of a page and reads it again after any change made in the app, so a
// step is ticked off as soon as it is done.
function useGuide(page: string) {
  const [epoch, setEpoch] = useState(0)
  useEffect(() => {
    let timer = 0
    const off = onChange(() => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => setEpoch((e) => e + 1), 400)
    })
    return () => {
      off()
      window.clearTimeout(timer)
    }
  }, [])
  return useResource<Onboarding>(`/api/guides/${encodeURIComponent(page)}`, epoch)
}

// SetupGuide is the step-by-step setup of a page, in the style of the «first steps» checklist:
// what to do, in order, for the page to be of use. It hides once every required step is done
// and can be folded to one line.
export function SetupGuide({ page, actions = {} }: { page: string; actions?: GuideActions }) {
  const t = useT(strings)
  const { data } = useGuide(page)
  const [collapsed, setCollapsed] = useState(() => savedCollapsed(page))
  if (!data || data.done) return null
  const required = data.steps.filter((s) => !s.optional)
  const done = required.filter((s) => s.done).length
  const current = data.steps.find((s) => !s.done && !s.optional)?.id
  const toggle = () => {
    const v = !collapsed
    setCollapsed(v)
    try {
      window.localStorage.setItem(collapsedKey(page), v ? '1' : '0')
    } catch {
      // the choice is not remembered
    }
  }
  const titleID = `sg-title-${page}`
  const progress = t('sg.progress', { done, total: required.length })

  if (collapsed) {
    return (
      <section className="card sg-card sg-folded" aria-labelledby={titleID}>
        <Wand2 size={18} className="sg-wand" aria-hidden />
        <h2 id={titleID}>{t('sg.title')}</h2>
        <span className="pill pill-warn ob-count">{progress}</span>
        <Button variant="ghost" className="sg-toggle" onClick={toggle} aria-expanded={false}>
          {t('sg.expand')}
          <ChevronDown size={15} aria-hidden />
        </Button>
      </section>
    )
  }
  return (
    <section className="card ob-card sg-card" aria-labelledby={titleID}>
      <header className="ob-head">
        <div className="sg-headline">
          <Wand2 size={20} className="sg-wand" aria-hidden />
          <div>
            <h2 id={titleID}>{t('sg.title')}</h2>
            <p className="muted">{t(`sg.${page}.intro`)}</p>
          </div>
        </div>
        <div className="sg-head-side">
          <span className="pill pill-warn ob-count">{progress}</span>
          <Button variant="ghost" className="sg-toggle" onClick={toggle} aria-expanded>
            {t('sg.collapse')}
            <ChevronUp size={15} aria-hidden />
          </Button>
        </div>
      </header>
      <div className="ob-bar" role="progressbar" aria-valuemin={0} aria-valuemax={required.length} aria-valuenow={done} aria-label={t('sg.title')}>
        <span style={{ width: `${(done / Math.max(required.length, 1)) * 100}%` }} />
      </div>
      <ol className="ob-steps">
        {data.steps.map((s, i) => (
          <Step key={s.id} page={page} n={i + 1} step={s} current={s.id === current} actions={actions} />
        ))}
      </ol>
    </section>
  )
}

function Step({ page, n, step: s, current, actions }: { page: string; n: number; step: OnboardingStep; current: boolean; actions: GuideActions }) {
  const t = useT(strings)
  const key = `sg.${page}.${s.id}`
  const act = s.action ? actions[s.action as GuideAction] : undefined
  return (
    <li className={`ob-step ${s.done ? 'ob-step-done' : ''} ${current ? 'sg-step-current' : ''}`} aria-current={current ? 'step' : undefined}>
      {s.done ? (
        <CheckCircle2 size={22} className="ob-icon ob-icon-done" aria-hidden />
      ) : (
        <span className="sg-num" aria-hidden>
          {n}
        </span>
      )}
      <div className="ob-text">
        <div className="ob-name">
          {t(key)}
          {s.optional && <span className="pill pill-off ob-opt">{t('sg.optional')}</span>}
        </div>
        {!s.done && <div className="muted">{t(`${key}.text`)}</div>}
      </div>
      <div className="ob-action">
        {s.done ? (
          <span className="pill pill-ok">{t('sg.done')}</span>
        ) : !s.can_fix ? (
          <span className="muted" title={t('sg.ask.hint')}>
            {t('sg.ask')}
          </span>
        ) : act ? (
          <Button variant={current ? 'primary' : 'secondary'} onClick={act}>
            {t(`${key}.go`)}
            <ChevronRight size={15} aria-hidden />
          </Button>
        ) : s.path ? (
          <Link to={s.path} className={`btn ${current ? 'btn-primary' : 'btn-secondary'}`}>
            {t(`${key}.go`)}
            <ChevronRight size={15} aria-hidden />
          </Link>
        ) : null}
      </div>
    </li>
  )
}
