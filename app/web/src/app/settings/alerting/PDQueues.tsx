import { useEffect, useState } from 'react'
import { ExternalLink, Link2 } from 'lucide-react'
import { api } from '../../../api'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, formatDate, Switch } from '../../../ui'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { strings } from './strings'
import type { PagerDutyView, QueueLinks, QueuesView } from './types'
import { Flash } from '../../../notify'

const tone: Record<string, string> = { active: 'ok', warning: 'warn', critical: 'error', maintenance: 'warn', disabled: 'off' }

type Props = {
  view: PagerDutyView
  // queues and onQueues: the switch of linking queues to teams, part of the unsaved form.
  queues: boolean
  onQueues: (v: boolean) => void
  dirty: boolean
  reloadKey: number
}

// PDQueues shows the queues of PagerDuty (its services): status, escalation policy, teams,
// open incidents and the Umbrella route sending to each, and links them to the teams.
export function PDQueues({ view, queues, onQueues, dirty, reloadKey }: Props) {
  const t = useT(strings)
  const action = useAction(strings)
  const { locale } = useLocale()
  const { can, timezone } = useSession()
  const [data, setData] = useState<QueuesView | null>(null)
  useEffect(() => {
    api<QueuesView>('GET', '/api/pagerduty/queues').then(setData, () => setData(null))
  }, [reloadKey])
  const routeName = (id: string) => (id === 'default' ? t('pd.oncall.default') : (view.routes.find((r) => r.id === id)?.name ?? id))
  const link = () =>
    action.run(async () => {
      const r = await api<{ links: QueueLinks; queues: QueuesView }>('POST', '/api/pagerduty/queues/link')
      setData(r.queues)
      const failed = Object.entries(r.links.failed ?? {})
        .map(([q, e]) => `${q}: ${e}`)
        .join('; ')
      const done = r.links.created.length ? t('pd.queues.created', { list: r.links.created.join(', ') }) : t('pd.queues.nothing')
      return failed ? `${done} ${t('pd.queues.failed', { list: failed })}` : done
    })
  const gone = (data?.gone ?? []).map(routeName)

  return (
    <section className="al-section">
      <h3 className="al-sub">{t('pd.queues')}</h3>
      <p className="hint">{t('pd.queues.hint')}</p>
      <Switch checked={queues} onChange={onQueues} label={t('pd.queues.auto')} hint={t('pd.queues.auto.hint')} />
      {data?.error && <Banner kind="error" title={t('pd.queues.error')}>{data.error}</Banner>}
      {action.error && <Flash kind="error" title={action.error.message}>{action.error.detail}</Flash>}
      {action.notice && <Flash kind="ok" title={action.notice} />}
      {gone.length > 0 && <Banner kind="warn" title={t('pd.queues.gone', { list: gone.join(', ') })} />}
      {data && data.queues.length > 0 ? (
        <div className="cn-table-wrap">
          <table className="cn-table compact">
            <thead>
              <tr>
                <th>{t('pd.queues.name')}</th>
                <th>{t('state')}</th>
                <th>{t('pd.queues.policy')}</th>
                <th>{t('pd.queues.teams')}</th>
                <th>{t('pd.queues.open')}</th>
                <th>{t('pd.queues.route')}</th>
              </tr>
            </thead>
            <tbody>
              {data.queues.map((q) => (
                <tr key={q.id}>
                  <td>
                    <a href={q.html_url} target="_blank" rel="noopener noreferrer">
                      {q.name} <ExternalLink size={12} aria-hidden />
                    </a>
                  </td>
                  <td>
                    <span className={`pill pill-${tone[q.status] ?? 'off'}`}>{t(`pd.queues.status.${q.status}`)}</span>
                  </td>
                  <td>{q.policy || '—'}</td>
                  <td>{q.teams.map((x) => x.name).join(', ') || '—'}</td>
                  <td>{t('pd.queues.counts', { triggered: q.triggered, acknowledged: q.acknowledged })}</td>
                  <td>{q.routes.length ? q.routes.map(routeName).join(', ') : <span className="muted">{t('pd.queues.unlinked')}</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="hint">{t('pd.queues.none')}</p>
      )}
      <div className="row">
        {can('settings.alerting:edit') && (
          <Button busy={action.busy} disabled={dirty} title={dirty ? t('nt.test.saveFirst') : undefined} onClick={() => void link()}>
            <Link2 size={15} aria-hidden />
            {t('pd.queues.link')}
          </Button>
        )}
        {data?.at && <span className="hint">{t('pd.queues.at', { at: formatDate(data.at, locale, timezone) })}</span>}
      </div>
    </section>
  )
}
