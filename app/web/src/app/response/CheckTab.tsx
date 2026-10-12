import { useState } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner, Button, Field, Segmented, Select, Stepper } from '../../ui'
import { severityText, type Severity } from '../incidents/types'
import { ProfileCard } from '../profile/ProfileCard'
import { useAction } from '../profile/useAction'
import { AssessmentView } from './parts'
import { responseStrings } from './strings'
import { SEVERITIES, type Simulation } from './types'

// CheckTab assesses a made-up incident with the saved settings.
export function CheckTab({ services, dirty }: { services: { id: string; name: string }[]; dirty: boolean }) {
  const t = useT(responseStrings)
  const [service, setService] = useState(services[0]?.id ?? '')
  const [severity, setSeverity] = useState<Severity>('error')
  const [method, setMethod] = useState<'red' | 'use' | 'other'>('red')
  const [incidents, setIncidents] = useState(1)
  const [out, setOut] = useState<Simulation | null>(null)
  const runner = useAction(responseStrings)
  const run = () =>
    runner.run(async () => {
      setOut(await api<Simulation>('POST', '/api/response/simulate', { service_id: service, severity, method, incidents }))
    })
  return (
    <ProfileCard title={t('rs.tab.check')} action={runner} onSubmit={run} wide footer={<Button type="submit" variant="primary" busy={runner.busy}>{t('rs.sim.run')}</Button>}>
      <p className="muted">{t('rs.sim.text')}</p>
      {dirty && <Banner kind="warn" title={t('rs.sim.unsaved')} />}
      <div className="grid-2">
        <Field label={t('rs.sim.service')}>
          {(id) => (
            <Select id={id} value={service} onChange={(e) => setService(e.target.value)}>
              <option value="">{t('rs.sim.none')}</option>
              {services.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label={t('rs.sim.severity')}>
          {(id) => (
            <Select id={id} value={severity} onChange={(e) => setSeverity(e.target.value as Severity)}>
              {SEVERITIES.map((s) => (
                <option key={s} value={s}>
                  {severityText(t, s)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label={t('rs.sim.method')}>
          {() => <Segmented label={t('rs.sim.method')} value={method} onChange={setMethod} options={[{ value: 'red', label: 'RED' }, { value: 'use', label: 'USE' }, { value: 'other', label: '—' }]} />}
        </Field>
        <Field label={t('rs.sim.incidents')}>{(id) => <Stepper id={id} value={incidents} min={1} max={100} onChange={setIncidents} />}</Field>
      </div>
      {out && (
        <div className="rs-sim">
          <AssessmentView as={out.assessment} />
          {!out.policy && <Banner kind="info" title={t('rs.sim.no_policy')} />}
          {out.policy && (
            <ol className="rs-sim-steps">
              {out.steps.map((s, i) => (
                <li key={i}>
                  <b>{t('rs.step', { n: i + 1 })}</b> · +{s.after_minutes}′ · {s.methods.map((m) => t(`method.${m}`)).join(', ')} → {s.people.length ? s.people.join(', ') : t('rs.sim.nobody')}
                </li>
              ))}
              {out.policy.war_room.enabled && <li>{t('rs.sim.room', { people: out.room.length ? out.room.join(', ') : t('rs.sim.nobody') })}</li>}
            </ol>
          )}
        </div>
      )}
    </ProfileCard>
  )
}
