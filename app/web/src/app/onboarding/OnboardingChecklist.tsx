import type { ReactNode } from 'react'
import { ChevronRight, CheckCircle2, Circle } from 'lucide-react'
import { useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Link } from '../../router'
import { strings } from './strings'
import type { Onboarding, OnboardingStep } from './types'
import './onboarding.css'

// useOnboarding loads the «first steps» checklist of the signed-in user.
export function useOnboarding(epoch = 0) {
  return useResource<Onboarding>('/api/onboarding', epoch)
}

// OnboardingChecklist shows the steps from installation to the first incident reaching a person.
// It shows `fallback` while loading, when the checklist cannot be read and once every required
// step is done.
export function OnboardingChecklist({ epoch = 0, fallback = null, incidentsNote }: { epoch?: number; fallback?: ReactNode; incidentsNote?: boolean }) {
  const t = useT(strings)
  const { data } = useOnboarding(epoch)
  if (!data || data.done) return <>{fallback}</>
  const required = data.steps.filter((s) => !s.optional)
  const done = required.filter((s) => s.done).length
  return (
    <section className="card ob-card" aria-labelledby="ob-title">
      <header className="ob-head">
        <div>
          <h2 id="ob-title">{t('ob.title')}</h2>
          <p className="muted">{t('ob.lead')}</p>
        </div>
        <span className="pill pill-warn ob-count">{t('ob.progress', { done, total: required.length })}</span>
      </header>
      <div className="ob-bar" role="progressbar" aria-valuemin={0} aria-valuemax={required.length} aria-valuenow={done} aria-label={t('ob.title')}>
        <span style={{ width: `${(done / Math.max(required.length, 1)) * 100}%` }} />
      </div>
      <ol className="ob-steps">
        {data.steps.map((s) => (
          <Step key={s.id} step={s} />
        ))}
      </ol>
      {incidentsNote && <p className="muted ob-foot">{t('ob.empty')}</p>}
    </section>
  )
}

function Step({ step: s }: { step: OnboardingStep }) {
  const t = useT(strings)
  return (
    <li className={`ob-step ${s.done ? 'ob-step-done' : ''}`}>
      {s.done ? <CheckCircle2 size={20} className="ob-icon ob-icon-done" aria-hidden /> : <Circle size={20} className="ob-icon" aria-hidden />}
      <div className="ob-text">
        <div className="ob-name">
          {t(`ob.step.${s.id}`)}
          {s.optional && <span className="pill pill-off ob-opt">{t('ob.optional')}</span>}
        </div>
        {!s.done && <div className="muted">{t(`ob.step.${s.id}.text`)}</div>}
      </div>
      <div className="ob-action">
        {s.done ? (
          <span className="pill pill-ok">{t('ob.done')}</span>
        ) : s.can_fix ? (
          <Link to={s.path} className="btn btn-secondary">
            {t(`ob.step.${s.id}.go`)}
            <ChevronRight size={15} aria-hidden />
          </Link>
        ) : (
          <span className="muted" title={t('ob.ask.hint')}>
            {t('ob.ask')}
          </span>
        )}
      </div>
    </li>
  )
}
