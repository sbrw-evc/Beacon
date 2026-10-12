import { useState, type ReactNode } from 'react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner, Button, Field, Input, Password, Segmented } from '../../ui'
import { severityText } from '../incidents/types'
import { ProfileCard } from '../profile/ProfileCard'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import { responseStrings } from './strings'
import { SEVERITIES, type Mode, type ResponseView } from './types'
import { VoiceCard } from './VoiceCard'

type Props = { view: ResponseView; onSaved: (v: ResponseView) => void }

export function IntegrationsTab({ view, onSaved }: Props) {
  const t = useT(responseStrings)
  return (
    <>
      <Banner kind="info" title={t('rs.int.text')} />
      <JiraCard view={view} onSaved={onSaved} />
      <GraphCard view={view} onSaved={onSaved} />
      <ZoomCard view={view} onSaved={onSaved} />
      <VoiceCard view={view} onSaved={onSaved} />
    </>
  )
}

// IntegrationCard is the frame of one integration: the mode, the fields, save and check.
function IntegrationCard({
  title,
  text,
  kind,
  mode,
  onMode,
  dirty,
  onSave,
  saver,
  children,
}: {
  title: string
  text: string
  kind: 'jira' | 'graph' | 'zoom'
  mode: Mode
  onMode: (m: Mode) => void
  dirty: boolean
  onSave: () => void
  saver: ReturnType<typeof useAction>
  children: ReactNode
}) {
  const t = useT(responseStrings)
  const { can } = useSession()
  const canEdit = can('response:edit')
  const tester = useAction(responseStrings)
  const test = () =>
    tester.run(async () => {
      const r = await api<{ info: string }>('POST', `/api/response/test/${kind}`)
      return t('rs.int.ok', { info: r.info })
    })
  return (
    <ProfileCard
      title={title}
      action={tester.error || tester.notice ? tester : saver}
      onSubmit={onSave}
      wide
      footer={
        <>
          {can('response:test') && (
            <Button busy={tester.busy} disabled={dirty} onClick={() => void test()}>
              {t('rs.int.test')}
            </Button>
          )}
          {canEdit && (
            <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
              {t('rs.save')}
            </Button>
          )}
        </>
      }
    >
      <p className="muted">{text}</p>
      <fieldset className="plain-fieldset" disabled={!canEdit}>
        <Segmented label={t('rs.int.mode')} value={mode} onChange={onMode} options={(['off', 'dry_run', 'live'] as Mode[]).map((m) => ({ value: m, label: t(`rs.mode.${m}`) }))} />
        <div className="grid-2 rs-int-grid">{children}</div>
      </fieldset>
    </ProfileCard>
  )
}

function secretHint(t: (k: string) => string, has: boolean, hint?: string) {
  return [has ? t('rs.int.secret.set') : '', hint ?? ''].filter(Boolean).join(' ')
}

function JiraCard({ view, onSaved }: Props) {
  const t = useT(responseStrings)
  const j = view.jira
  const init = {
    mode: j.mode,
    base_url: j.base_url,
    email: j.email,
    token: '',
    project: j.project,
    task_type: j.task_type,
    postmortem_type: j.postmortem_type,
    labels: (j.labels ?? []).join(' '),
    link_type: j.link_type,
    done_transition: j.done_transition,
    priorities: { ...j.priorities },
  }
  const [d, setD] = useState(init)
  const saver = useAction(responseStrings)
  const dirty = JSON.stringify(d) !== JSON.stringify(init)
  const save = () =>
    saver.run(async () => {
      onSaved(await api<ResponseView>('PUT', '/api/response/jira', { ...d, labels: d.labels.split(/\s+/).filter(Boolean) }))
      return t('rs.saved')
    })
  return (
    <IntegrationCard title={t('rs.jira.title')} text="" kind="jira" mode={d.mode} onMode={(mode) => setD({ ...d, mode })} dirty={dirty} onSave={save} saver={saver}>
      <Field label={t('rs.jira.site')}>{(id) => <Input id={id} value={d.base_url} placeholder="https://example.atlassian.net" onChange={(e) => setD({ ...d, base_url: e.target.value })} />}</Field>
      <Field label={t('rs.jira.project')}>{(id) => <Input id={id} value={d.project} placeholder="OPS" onChange={(e) => setD({ ...d, project: e.target.value.toUpperCase() })} />}</Field>
      <Field label={t('rs.jira.email')}>{(id) => <Input id={id} value={d.email} placeholder="umbrella-bot@example.com" onChange={(e) => setD({ ...d, email: e.target.value })} />}</Field>
      <Field label={t('rs.jira.token')} hint={secretHint(t, j.has_token, t('rs.jira.token.hint'))}>
        {(id) => <Password id={id} value={d.token} autoComplete="new-password" onChange={(e) => setD({ ...d, token: e.target.value })} />}
      </Field>
      <Field label={t('rs.jira.task_type')}>{(id) => <Input id={id} value={d.task_type} onChange={(e) => setD({ ...d, task_type: e.target.value })} />}</Field>
      <Field label={t('rs.jira.pm_type')}>{(id) => <Input id={id} value={d.postmortem_type} onChange={(e) => setD({ ...d, postmortem_type: e.target.value })} />}</Field>
      <Field label={t('rs.jira.labels')} hint={t('rs.jira.labels.hint')}>
        {(id) => <Input id={id} value={d.labels} onChange={(e) => setD({ ...d, labels: e.target.value })} />}
      </Field>
      <Field label={t('rs.jira.link')}>{(id) => <Input id={id} value={d.link_type} placeholder="Relates" onChange={(e) => setD({ ...d, link_type: e.target.value })} />}</Field>
      <Field label={t('rs.jira.done')} hint={t('rs.jira.done.hint')}>
        {(id) => <Input id={id} value={d.done_transition} placeholder="Done" onChange={(e) => setD({ ...d, done_transition: e.target.value })} />}
      </Field>
      {SEVERITIES.map((s) => (
        <Field key={s} label={t('rs.jira.prio', { p: severityText(t, s) })}>
          {(id) => <Input id={id} value={d.priorities[s] ?? ''} onChange={(e) => setD({ ...d, priorities: { ...d.priorities, [s]: e.target.value } })} />}
        </Field>
      ))}
    </IntegrationCard>
  )
}

function GraphCard({ view, onSaved }: Props) {
  const t = useT(responseStrings)
  const g = view.graph
  const init = { mode: g.mode, tenant_id: g.tenant_id, client_id: g.client_id, client_secret: '', refresh_token: '', account: g.account }
  const [d, setD] = useState(init)
  const saver = useAction(responseStrings)
  const dirty = JSON.stringify(d) !== JSON.stringify(init)
  const save = () =>
    saver.run(async () => {
      onSaved(await api<ResponseView>('PUT', '/api/response/graph', { ...d, login_url: g.login_url ?? '', graph_url: g.graph_url ?? '' }))
      return t('rs.saved')
    })
  return (
    <IntegrationCard title={t('rs.graph.title')} text={t('rs.graph.text')} kind="graph" mode={d.mode} onMode={(mode) => setD({ ...d, mode })} dirty={dirty} onSave={save} saver={saver}>
      <Field label={t('rs.graph.tenant')}>{(id) => <Input id={id} value={d.tenant_id} placeholder="contoso.onmicrosoft.com" onChange={(e) => setD({ ...d, tenant_id: e.target.value })} />}</Field>
      <Field label={t('rs.graph.client')}>{(id) => <Input id={id} value={d.client_id} onChange={(e) => setD({ ...d, client_id: e.target.value })} />}</Field>
      <Field label={t('rs.graph.secret')} hint={secretHint(t, g.has_secret)}>
        {(id) => <Password id={id} value={d.client_secret} autoComplete="new-password" onChange={(e) => setD({ ...d, client_secret: e.target.value })} />}
      </Field>
      <Field label={t('rs.graph.refresh')} hint={secretHint(t, g.has_refresh, t('rs.graph.refresh.hint'))}>
        {(id) => <Password id={id} value={d.refresh_token} autoComplete="new-password" onChange={(e) => setD({ ...d, refresh_token: e.target.value })} />}
      </Field>
      <Field label={t('rs.graph.account')}>{(id) => <Input id={id} value={d.account} placeholder="umbrella@contoso.com" onChange={(e) => setD({ ...d, account: e.target.value })} />}</Field>
    </IntegrationCard>
  )
}

function ZoomCard({ view, onSaved }: Props) {
  const t = useT(responseStrings)
  const z = view.zoom
  const init = { mode: z.mode, account_id: z.account_id, client_id: z.client_id, client_secret: '', user: z.user }
  const [d, setD] = useState(init)
  const saver = useAction(responseStrings)
  const dirty = JSON.stringify(d) !== JSON.stringify(init)
  const save = () =>
    saver.run(async () => {
      onSaved(await api<ResponseView>('PUT', '/api/response/zoom', d))
      return t('rs.saved')
    })
  return (
    <IntegrationCard title={t('rs.zoom.title')} text={t('rs.zoom.text')} kind="zoom" mode={d.mode} onMode={(mode) => setD({ ...d, mode })} dirty={dirty} onSave={save} saver={saver}>
      <Field label={t('rs.zoom.account')}>{(id) => <Input id={id} value={d.account_id} onChange={(e) => setD({ ...d, account_id: e.target.value })} />}</Field>
      <Field label={t('rs.zoom.client')}>{(id) => <Input id={id} value={d.client_id} onChange={(e) => setD({ ...d, client_id: e.target.value })} />}</Field>
      <Field label={t('rs.zoom.secret')} hint={secretHint(t, z.has_secret)}>
        {(id) => <Password id={id} value={d.client_secret} autoComplete="new-password" onChange={(e) => setD({ ...d, client_secret: e.target.value })} />}
      </Field>
      <Field label={t('rs.zoom.user')} hint={t('rs.zoom.user.hint')}>
        {(id) => <Input id={id} value={d.user} onChange={(e) => setD({ ...d, user: e.target.value })} />}
      </Field>
    </IntegrationCard>
  )
}
