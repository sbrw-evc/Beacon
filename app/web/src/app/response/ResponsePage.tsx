import { useEffect, useMemo, useState } from 'react'
import { api } from '../../api'
import { useResource } from '../../connections/useRequest'
import { useLocale, useT } from '../../i18n'
import { Banner, Button, formatDate, Segmented } from '../../ui'
import { ProfileCard } from '../profile/ProfileCard'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import '../incidents/incidents.css'
import { CheckTab } from './CheckTab'
import { ImpactTab } from './ImpactTab'
import { IntegrationsTab } from './IntegrationsTab'
import { PoliciesTab } from './PoliciesTab'
import './response.css'
import { responseStrings } from './strings'
import type { ImpactPolicy, Mode, Policy, ResponseView } from './types'
import { SetupGuide } from '../guide/SetupGuide'

type Tab = 'policies' | 'impact' | 'integrations' | 'check'

export type Refs = { users: { id: string; name: string; username: string; disabled: boolean }[]; teams: { id: string; name: string }[]; services: { id: string; name: string }[] }

type Draft = { mode: Mode; impact: ImpactPolicy; policies: Policy[] }

const KEY = 'umbrella.response.tab'

function savedTab(): Tab {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'policies' || v === 'impact' || v === 'integrations' || v === 'check') return v
  } catch {
    // no storage: the first tab
  }
  return 'policies'
}

export function ResponsePage() {
  const t = useT(responseStrings)
  const { locale } = useLocale()
  const { can } = useSession()
  const canEdit = can('response:edit')
  const [view, setView] = useState<ResponseView | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [tab, setTabState] = useState<Tab>(savedTab)
  const refsRes = useResource<Refs>('/api/refs', 0)
  const loader = useAction(responseStrings)
  const saver = useAction(responseStrings)
  const setTab = (v: Tab) => {
    setTabState(v)
    try {
      localStorage.setItem(KEY, v)
    } catch {
      // the tab is not remembered
    }
  }
  const apply = (v: ResponseView) => {
    setView(v)
    setDraft({ mode: v.mode, impact: structuredClone(v.impact), policies: structuredClone(v.policies) })
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<ResponseView>('GET', '/api/response')))
  }, [load])
  const refs = useMemo<Refs>(() => {
    const r = refsRes.data
    return {
      users: (r?.users ?? []).filter((u) => !u.disabled).map((u) => ({ ...u, name: u.name || u.username })),
      teams: r?.teams ?? [],
      services: r?.services ?? [],
    }
  }, [refsRes.data])

  if (!view || !draft) {
    return (
      <ProfileCard title={t('rs.mode')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const dirty = JSON.stringify(draft) !== JSON.stringify({ mode: view.mode, impact: view.impact, policies: view.policies })
  const save = () =>
    saver.run(async () => {
      apply(await api<ResponseView>('PUT', '/api/response', draft))
      return t('rs.saved')
    })
  const offChannels = Object.entries(view.channels)
    .filter(([, on]) => !on)
    .map(([c]) => t(`method.${c}`))
  const policyTab = tab === 'policies' || tab === 'impact'
  // Voice methods work when voice is on and the integration they go through is.
  const voiceOn = view.voice.mode !== 'off'
  const channels = {
    ...view.channels,
    voice_teams: voiceOn && view.graph.mode !== 'off',
    voice_telegram: voiceOn && !!view.channels.telegram,
    voice_zoom: voiceOn && view.zoom.mode !== 'off',
  }
  return (
    <div className="rs-page">
      <SetupGuide page="response" actions={{ integrations: () => setTab('integrations') }} />
      <ProfileCard
        title={t('rs.mode')}
        action={saver}
        onSubmit={save}
        wide
        footer={
          canEdit &&
          policyTab && (
            <>
              <Button
                onClick={() => setDraft(tab === 'impact' ? { ...draft, impact: structuredClone(view.default_impact) } : { ...draft, policies: structuredClone(view.default_policies) })}
              >
                {t('rs.reset')}
              </Button>
              <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
                {t('rs.save')}
              </Button>
            </>
          )
        }
      >
        <fieldset className="plain-fieldset rs-mode" disabled={!canEdit}>
          <Segmented
            label={t('rs.mode')}
            value={draft.mode}
            onChange={(mode) => setDraft({ ...draft, mode })}
            options={(['off', 'dry_run', 'live'] as Mode[]).map((m) => ({ value: m, label: t(`rs.mode.${m}`) }))}
          />
          <p className="muted">{t(`rs.mode.hint.${draft.mode}`)}</p>
          {view.mode !== 'off' && view.active_since && <p className="hint">{t('rs.since', { at: formatDate(view.active_since, locale) })}</p>}
          {view.updated_at && <p className="hint">{t('rs.updated', { at: formatDate(view.updated_at, locale), by: view.updated_by ?? '' })}</p>}
        </fieldset>
        {offChannels.length > 0 && <Banner kind="warn" title={t('rs.channels.off', { list: offChannels.join(', ') })} />}
        <Segmented
          label={t('rs.mode')}
          value={tab}
          onChange={setTab}
          options={[
            { value: 'policies', label: t('rs.tab.policies') },
            { value: 'impact', label: t('rs.tab.impact') },
            { value: 'integrations', label: t('rs.tab.integrations') },
            { value: 'check', label: t('rs.tab.check') },
          ]}
        />
        {tab === 'policies' && <PoliciesTab policies={draft.policies} onChange={(policies) => setDraft({ ...draft, policies })} refs={refs} channels={channels} disabled={!canEdit} />}
        {tab === 'impact' && <ImpactTab impact={draft.impact} onChange={(impact) => setDraft({ ...draft, impact })} disabled={!canEdit} />}
      </ProfileCard>
      {tab === 'integrations' && <IntegrationsTab view={view} onSaved={apply} />}
      {tab === 'check' && <CheckTab services={refs.services} dirty={dirty} />}
    </div>
  )
}
