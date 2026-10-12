import { useState } from 'react'
import { ChevronDown, Plus, Trash2 } from 'lucide-react'
import { useT } from '../../i18n'
import { Button, Field, Segmented, Stepper, Switch } from '../../ui'
import { SeverityPill } from '../incidents/IncidentDetail'
import { PickList, Toggles } from './parts'
import type { Refs } from './ResponsePage'
import { responseStrings } from './strings'
import { METHODS, TARGETS, type Method, type Policy, type Step } from './types'

type Props = { policies: Policy[]; onChange: (p: Policy[]) => void; refs: Refs; channels: Record<string, boolean>; disabled: boolean }

export function PoliciesTab({ policies, onChange, refs, channels, disabled }: Props) {
  const [open, setOpen] = useState<string>(policies[0]?.priority ?? '')
  const set = (i: number, p: Policy) => onChange(policies.map((x, j) => (j === i ? p : x)))
  return (
    <div className="rs-policies">
      {policies.map((p, i) => (
        <PolicyCard key={p.priority} p={p} open={open === p.priority} onToggle={() => setOpen(open === p.priority ? '' : p.priority)} onChange={(v) => set(i, v)} refs={refs} channels={channels} disabled={disabled} />
      ))}
    </div>
  )
}

function summary(t: (k: string, v?: Record<string, string | number>) => string, p: Policy) {
  const jira = p.jira.task && p.jira.postmortem ? 'both' : p.jira.task ? 'task' : p.jira.postmortem ? 'pm' : 'none'
  return t('rs.policy.summary', {
    steps: p.steps.length,
    room: t(p.war_room.enabled ? 'rs.policy.room.on' : 'rs.policy.room.off'),
    bridge: t(`rs.policy.bridge.${p.bridge || 'none'}`),
    jira: t(`rs.policy.jira.${jira}`),
  })
}

function PolicyCard({ p, open, onToggle, onChange, refs, channels, disabled }: { p: Policy; open: boolean; onToggle: () => void; onChange: (p: Policy) => void } & Omit<Props, 'policies' | 'onChange'>) {
  const t = useT(responseStrings)
  const setStep = (i: number, s: Step) => onChange({ ...p, steps: p.steps.map((x, j) => (j === i ? s : x)) })
  const addStep = () => {
    const last = p.steps[p.steps.length - 1]
    onChange({ ...p, steps: [...p.steps, { after_minutes: last ? last.after_minutes + 15 : 0, targets: ['lead'], user_ids: [], team_ids: [], methods: ['email'] }] })
  }
  const methodOff = (m: Method) => m in channels && !channels[m]
  return (
    <section className={`rs-policy${p.enabled ? '' : ' rs-policy-off'}`}>
      <button type="button" className="rs-policy-head" aria-expanded={open} onClick={onToggle}>
        <SeverityPill severity={p.priority} />
        <span className="rs-policy-sum">{p.enabled ? summary(t, p) : t('rs.policy.off')}</span>
        <ChevronDown size={18} className={`rs-chev${open ? ' open' : ''}`} aria-hidden />
      </button>
      {open && (
        <fieldset className="plain-fieldset rs-policy-body" disabled={disabled}>
          <Switch checked={p.enabled} onChange={(enabled) => onChange({ ...p, enabled })} label={t('rs.policy.on')} />
          {p.enabled && (
            <>
              <h3 className="rs-h">{t('rs.steps')}</h3>
              <p className="hint">{t('rs.steps.hint')}</p>
              {p.steps.length === 0 && <p className="muted">{t('rs.step.none')}</p>}
              <ol className="rs-steps">
                {p.steps.map((s, i) => (
                  <li key={i} className="rs-step">
                    <div className="rs-step-head">
                      <b>{t('rs.step', { n: i + 1 })}</b>
                      <Field label={t('rs.step.after')}>{(id) => <Stepper id={id} value={s.after_minutes} min={0} max={10080} onChange={(after_minutes) => setStep(i, { ...s, after_minutes })} />}</Field>
                      {!disabled && (
                        <button type="button" className="icon-btn" aria-label={t('rs.step.remove')} title={t('rs.step.remove')} onClick={() => onChange({ ...p, steps: p.steps.filter((_, j) => j !== i) })}>
                          <Trash2 size={16} />
                        </button>
                      )}
                    </div>
                    <div className="rs-step-grid">
                      <span className="hint">{t('rs.step.targets')}</span>
                      <Toggles values={TARGETS} selected={s.targets} onChange={(targets) => setStep(i, { ...s, targets })} label={t('rs.step.targets')} word={(v) => t(`target.${v}`)} disabled={disabled} />
                      <span className="hint">{t('rs.step.users')}</span>
                      <PickList items={refs.users} selected={s.user_ids} onChange={(user_ids) => setStep(i, { ...s, user_ids })} label={t('rs.step.users')} disabled={disabled} />
                      <span className="hint">{t('rs.step.teams')}</span>
                      <PickList items={refs.teams} selected={s.team_ids} onChange={(team_ids) => setStep(i, { ...s, team_ids })} label={t('rs.step.teams')} disabled={disabled} />
                      <span className="hint">{t('rs.step.methods')}</span>
                      <Toggles values={METHODS} selected={s.methods} onChange={(methods) => setStep(i, { ...s, methods })} label={t('rs.step.methods')} word={(v) => t(`method.${v}`)} disabled={disabled} off={methodOff} />
                    </div>
                  </li>
                ))}
              </ol>
              {!disabled && p.steps.length < 10 && (
                <Button onClick={addStep}>
                  <Plus size={16} aria-hidden />
                  {t('rs.step.add')}
                </Button>
              )}
              <Switch checked={p.stop_on_ack} onChange={(stop_on_ack) => onChange({ ...p, stop_on_ack })} label={t('rs.stop_on_ack')} hint={t('rs.stop_on_ack.hint')} />

              <h3 className="rs-h">{t('rs.room')}</h3>
              <Switch checked={p.war_room.enabled} onChange={(enabled) => onChange({ ...p, war_room: { ...p.war_room, enabled } })} label={t('rs.room')} hint={t('rs.room.hint')} />
              {p.war_room.enabled && (
                <div className="rs-step-grid">
                  <span className="hint">{t('rs.room.members')}</span>
                  <Toggles values={TARGETS} selected={p.war_room.members} onChange={(members) => onChange({ ...p, war_room: { ...p.war_room, members } })} label={t('rs.room.members')} word={(v) => t(`target.${v}`)} disabled={disabled} />
                  <span className="hint">{t('rs.room.users')}</span>
                  <PickList items={refs.users} selected={p.war_room.user_ids} onChange={(user_ids) => onChange({ ...p, war_room: { ...p.war_room, user_ids } })} label={t('rs.room.users')} disabled={disabled} />
                  <span />
                  <div>
                    <Switch checked={p.war_room.add_escalated} onChange={(add_escalated) => onChange({ ...p, war_room: { ...p.war_room, add_escalated } })} label={t('rs.room.add_escalated')} />
                    <Switch checked={p.war_room.post_updates} onChange={(post_updates) => onChange({ ...p, war_room: { ...p.war_room, post_updates } })} label={t('rs.room.post_updates')} />
                  </div>
                </div>
              )}

              <h3 className="rs-h">{t('rs.bridge')}</h3>
              <p className="hint">{t('rs.bridge.hint')}</p>
              <Segmented
                label={t('rs.bridge')}
                value={p.bridge || 'none'}
                onChange={(v) => onChange({ ...p, bridge: v === 'none' ? '' : (v as Policy['bridge']) })}
                options={(['none', 'teams', 'zoom'] as const).map((v) => ({ value: v, label: t(`rs.bridge.${v}`) }))}
              />

              <h3 className="rs-h">{t('rs.jira')}</h3>
              <Switch checked={p.jira.task} onChange={(task) => onChange({ ...p, jira: { ...p.jira, task } })} label={t('rs.jira.task')} />
              <Switch checked={p.jira.comment} onChange={(comment) => onChange({ ...p, jira: { ...p.jira, comment } })} label={t('rs.jira.comment')} />
              <Switch
                checked={p.jira.postmortem}
                onChange={(postmortem) => onChange({ ...p, jira: { ...p.jira, postmortem } })}
                label={t('rs.jira.postmortem')}
                aside={
                  p.jira.postmortem && (
                    <Stepper value={p.jira.postmortem_days} min={1} max={90} label={t('rs.jira.days')} suffix={t('rs.days')} onChange={(postmortem_days) => onChange({ ...p, jira: { ...p.jira, postmortem_days } })} />
                  )
                }
              />
            </>
          )}
        </fieldset>
      )}
    </section>
  )
}
