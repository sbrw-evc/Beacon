import { useEffect, useState, type ReactNode } from 'react'
import { Eye, RotateCcw, Send } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../../api'
import { useLocale, useT } from '../../../i18n'
import { Banner, Button, Field, Input, Password, Select, Switch, Textarea } from '../../../ui'
import { ProfileCard } from '../../profile/ProfileCard'
import { SummaryCard } from '../../profile/SummaryCard'
import { useAction } from '../../profile/useAction'
import { useSession } from '../../session'
import { reveal } from './PagerDutyCard'
import { strings } from './strings'
import { severityText } from '../../incidents/types'
import { SEVERITIES, TEMPLATE_MESSAGES, TEMPLATE_PARTS, type BotStatus, type NotifyPreview, type NotifyView } from './types'
import { Flash } from '../../../notify'

type Channel = 'email' | 'telegram' | 'teams' | 'zoom'

// Delays offered for backup notification, in seconds; '' is automatic.
const DELAYS = [0, 60, 120, 300, 600, 900, 1800, 3600]

type Draft = {
  email: NotifyView['email'] & { password: string; port_text: string }
  telegram: { enabled: boolean; api_url: string; token: string; bot: boolean }
  teams: { enabled: boolean }
  zoom: { enabled: boolean; token: string }
  extra_emails: string
  extra_telegram: string
  extra_teams: string
  extra_zoom: string
  delay: string
  min_severity: string
  // templates: every template as edited, the built-in text where none is replaced.
  templates: Record<string, string>
}

function draftOf(v: NotifyView): Draft {
  return {
    email: { ...v.email, password: '', port_text: v.email.port ? String(v.email.port) : '' },
    telegram: { enabled: v.telegram.enabled, api_url: v.telegram.api_url ?? '', token: '', bot: !!v.telegram.bot },
    teams: { enabled: v.teams?.enabled ?? false },
    zoom: { enabled: v.zoom?.enabled ?? false, token: '' },
    extra_emails: v.extra_emails.join('\n'),
    extra_telegram: v.extra_telegram.join('\n'),
    extra_teams: (v.extra_teams ?? []).join('\n'),
    extra_zoom: (v.extra_zoom ?? []).join('\n'),
    delay: v.delay_seconds === null || v.delay_seconds === undefined ? '' : String(v.delay_seconds),
    min_severity: v.min_severity || 'error',
    templates: { ...(v.default_templates ?? {}), ...(v.templates ?? {}) },
  }
}

// overrides are the templates that differ from the built-in ones.
function overrides(templates: Record<string, string>, defaults: Record<string, string>) {
  return Object.fromEntries(Object.entries(templates).filter(([name, text]) => text !== (defaults[name] ?? '')))
}

const lines = (v: string) =>
  v
    .split(/[\n,;]/)
    .map((x) => x.trim())
    .filter(Boolean)

// urls: webhook URLs one per line (a URL may have ; or , in its query).
const urls = (v: string) =>
  v
    .split(/\s+/)
    .map((x) => x.trim())
    .filter(Boolean)

function bodyOf(d: Draft, defaults: Record<string, string>) {
  return {
    email: {
      enabled: d.email.enabled,
      host: d.email.host,
      port: Number(d.email.port_text) || 0,
      security: d.email.security,
      skip_verify: d.email.skip_verify,
      username: d.email.username,
      password: d.email.password,
      from: d.email.from,
    },
    telegram: { enabled: d.telegram.enabled, token: d.telegram.token, api_url: d.telegram.api_url, bot: d.telegram.bot },
    teams: d.teams,
    zoom: d.zoom,
    extra_emails: lines(d.extra_emails),
    extra_telegram: lines(d.extra_telegram),
    extra_teams: urls(d.extra_teams),
    extra_zoom: urls(d.extra_zoom),
    delay_seconds: d.delay === '' ? null : Number(d.delay),
    min_severity: d.min_severity,
    // Without the built-in templates (an older server) the saved ones are kept.
    templates: Object.keys(defaults).length ? overrides(d.templates, defaults) : undefined,
  }
}

export function NotifyCard() {
  const t = useT(strings)
  const { can } = useSession()
  const canEdit = can('settings.alerting:edit')
  const canTest = can('settings.alerting:test')
  const [view, setView] = useState<NotifyView | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const loader = useAction(strings)
  const saver = useAction(strings)
  const tester = useAction(strings)
  const [testTo, setTestTo] = useState<Record<Channel, string>>({ email: '', telegram: '', teams: '', zoom: '' })

  const apply = (v: NotifyView) => {
    setView(v)
    setDraft(draftOf(v))
  }
  const { run: load } = loader
  useEffect(() => {
    void load(async () => apply(await api<NotifyView>('GET', '/api/notifications')))
  }, [load])

  if (!view || !draft) {
    return (
      <ProfileCard title={t('nt.title')} action={loader} inline>
        {!loader.error && <p className="muted">{t('loading')}</p>}
      </ProfileCard>
    )
  }
  const defaults = view.default_templates ?? {}
  const dirty = JSON.stringify(bodyOf(draft, defaults)) !== JSON.stringify(bodyOf(draftOf(view), defaults))
  const setEmail = (p: Partial<Draft['email']>) => setDraft({ ...draft, email: { ...draft.email, ...p } })
  const setTg = (p: Partial<Draft['telegram']>) => setDraft({ ...draft, telegram: { ...draft.telegram, ...p } })
  const setZoom = (p: Partial<Draft['zoom']>) => setDraft({ ...draft, zoom: { ...draft.zoom, ...p } })
  const teamsOn = view.teams?.enabled ?? false
  const zoomOn = view.zoom?.enabled ?? false

  const save = () =>
    saver.run(async () => {
      apply(await api<NotifyView>('PUT', '/api/notifications', bodyOf(draft, defaults)))
      return t('saved')
    })
  const test = (channel: Channel) =>
    tester.run(async () => {
      const r = await api<{ to: string; bot?: string }>('POST', '/api/notifications/test', { channel, to: testTo[channel] })
      return r.bot ? t('nt.test.okBot', { to: r.to, bot: r.bot }) : t('nt.test.ok', { to: r.to })
    })

  // botText: the state of the bot built into Umbrella, with its name and last error.
  const botText = (b?: BotStatus) => {
    if (!b) return t('nt.bot.state.off')
    const parts = [t(`nt.bot.state.${b.state}`)]
    if (b.username) parts.push(`@${b.username}`)
    if (b.state === 'error' && b.last_error) parts.push(b.last_error)
    return <span className={b.state === 'error' ? 'al-error' : undefined}>{parts.join(' · ')}</span>
  }
  const autoText = t(view.pd_enabled ? 'nt.delay.auto.pd' : 'nt.delay.auto.nopd')
  const delayText = (v: number | null | undefined) =>
    v === null || v === undefined ? t('nt.delay.auto', { auto: autoText }) : v === 0 ? t('nt.delay.now') : v % 60 === 0 ? t('nt.delay.min', { n: v / 60 }) : `${v} s`
  const delays = draft.delay !== '' && !DELAYS.includes(Number(draft.delay)) ? [...DELAYS, Number(draft.delay)].sort((x, y) => x - y) : DELAYS
  const on = view.email.enabled || view.telegram.enabled || teamsOn || zoomOn
  const hooks = (view.extra_teams?.length ?? 0) + (view.extra_zoom?.length ?? 0)
  const extra = [
    view.extra_emails.length ? t('nt.extra.n.emails', { n: view.extra_emails.length }) : '',
    view.extra_telegram.length ? t('nt.extra.n.chats', { n: view.extra_telegram.length }) : '',
    hooks ? t('nt.extra.n.hooks', { n: hooks }) : '',
  ]
    .filter(Boolean)
    .join(', ')
  const rows: [string, ReactNode][] = [
    [t('nt.email'), view.email.enabled ? [t('pd.state.on'), view.email.host].filter(Boolean).join(' · ') : t('pd.state.off')],
    [t('nt.telegram'), t(view.telegram.enabled ? 'pd.state.on' : 'pd.state.off')],
    ...(view.telegram.enabled && view.telegram.bot ? [[t('nt.bot'), botText(view.bot_status)] as [string, ReactNode]] : []),
    [t('nt.teams'), t(teamsOn ? 'pd.state.on' : 'pd.state.off')],
    [t('nt.zoom'), t(zoomOn ? 'pd.state.on' : 'pd.state.off')],
    [t('nt.extra'), extra],
    [t('nt.summary.when'), t('nt.summary.when.value', { delay: delayText(view.delay_seconds), severity: severityText(t, view.min_severity || 'error') })],
    [t('nt.tpl'), Object.keys(view.templates ?? {}).length ? t('nt.tpl.changed', { n: Object.keys(view.templates ?? {}).length }) : t('nt.tpl.builtin')],
  ]

  return (
    <>
      <SummaryCard title={t('nt.title')} badge={<span className={`pill pill-${on ? 'ok' : 'off'}`}>{t(on ? 'pd.state.on' : 'pd.state.off')}</span>} text={t('nt.text')} rows={rows}>
        {!view.links && <Banner kind="info" title={t('nt.links.off')} />}
      </SummaryCard>
      <ProfileCard
        title={t('nt.settings')}
        action={saver}
        onSubmit={save}
        footer={
          canEdit && (
            <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
              {t('save')}
            </Button>
          )
        }
      >
        <fieldset className="plain-fieldset al-grid" disabled={!canEdit}>
          <div className="al-col stack">
            <h3 className="al-sub">{t('nt.email')}</h3>
            <Switch checked={draft.email.enabled} onChange={(enabled) => setEmail({ enabled })} label={t('nt.email.enable')} />
            <Reveal open={draft.email.enabled}>
              <div className="grid-2">
                <Field label={t('nt.host')}>{(id) => <Input id={id} value={draft.email.host} placeholder="smtp.example.com" onChange={(e) => setEmail({ host: e.target.value })} />}</Field>
                <Field label={t('nt.port')} hint={t('nt.port.hint')}>
                  {(id) => <Input id={id} value={draft.email.port_text} inputMode="numeric" placeholder="587" onChange={(e) => setEmail({ port_text: e.target.value.replace(/\D/g, '') })} />}
                </Field>
              </div>
              <Field label={t('nt.security')}>
                {(id) => (
                  <Select id={id} value={draft.email.security} onChange={(e) => setEmail({ security: e.target.value as Draft['email']['security'] })}>
                    {(['starttls', 'tls', 'none'] as const).map((s) => (
                      <option key={s} value={s}>
                        {t(`nt.security.${s}`)}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              {draft.email.security !== 'none' && <Switch checked={draft.email.skip_verify} onChange={(skip_verify) => setEmail({ skip_verify })} label={t('nt.skip')} />}
              <div className="grid-2">
                <Field label={t('nt.user')}>
                  {(id) => <Input id={id} value={draft.email.username} autoComplete="off" onChange={(e) => setEmail({ username: e.target.value })} />}
                </Field>
                <Field label={t('nt.password')} hint={view.has_password && !draft.email.password ? t('keep') : undefined}>
                  {(id) => <Password id={id} value={draft.email.password} autoComplete="new-password" onChange={(e) => setEmail({ password: e.target.value })} />}
                </Field>
              </div>
              <Field label={t('nt.from')} hint={t('nt.from.hint')}>
                {(id) => <Input id={id} value={draft.email.from} onChange={(e) => setEmail({ from: e.target.value })} />}
              </Field>
            </Reveal>
          </div>
          <div className="al-col stack">
            <h3 className="al-sub">{t('nt.telegram')}</h3>
            <Switch checked={draft.telegram.enabled} onChange={(enabled) => setTg({ enabled })} label={t('nt.telegram.enable')} />
            <Reveal open={draft.telegram.enabled}>
              <Field label={t('nt.token')} hint={view.has_token && !draft.telegram.token ? t('keep') : t('nt.token.hint')}>
                {(id) => <Password id={id} value={draft.telegram.token} autoComplete="off" placeholder="123456:ABC…" onChange={(e) => setTg({ token: e.target.value })} />}
              </Field>
              <Field label={t('nt.api')} hint={t('nt.api.hint')}>
                {(id) => <Input id={id} value={draft.telegram.api_url} placeholder="https://api.telegram.org" onChange={(e) => setTg({ api_url: e.target.value })} />}
              </Field>
              <Switch checked={draft.telegram.bot} onChange={(bot) => setTg({ bot })} label={t('nt.bot.enable')} hint={t('nt.bot.hint')} />
            </Reveal>
          </div>
          <div className="al-col stack">
            <h3 className="al-sub">{t('nt.teams')}</h3>
            <Switch checked={draft.teams.enabled} onChange={(enabled) => setDraft({ ...draft, teams: { enabled } })} label={t('nt.teams.enable')} />
            <Reveal open={draft.teams.enabled}>
              <p className="hint">{t('nt.teams.hint')}</p>
            </Reveal>
          </div>
          <div className="al-col stack">
            <h3 className="al-sub">{t('nt.zoom')}</h3>
            <Switch checked={draft.zoom.enabled} onChange={(enabled) => setZoom({ enabled })} label={t('nt.zoom.enable')} />
            <Reveal open={draft.zoom.enabled}>
              <Field label={t('nt.zoom.token')} hint={view.has_zoom_token && !draft.zoom.token ? t('keep') : t('nt.zoom.token.hint')}>
                {(id) => <Password id={id} value={draft.zoom.token} autoComplete="off" onChange={(e) => setZoom({ token: e.target.value })} />}
              </Field>
              <p className="hint">{t('nt.zoom.hint')}</p>
            </Reveal>
          </div>
        </fieldset>
        <section className="al-section">
          <h3 className="al-sub">{t('nt.when')}</h3>
          <fieldset className="plain-fieldset grid-2" disabled={!canEdit}>
            <Field label={t('nt.delay')} hint={t('nt.delay.hint')}>
              {(id) => (
                <Select id={id} value={draft.delay} onChange={(e) => setDraft({ ...draft, delay: e.target.value })}>
                  <option value="">{t('nt.delay.auto', { auto: autoText })}</option>
                  {delays.map((d) => (
                    <option key={d} value={String(d)}>
                      {delayText(d)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label={t('nt.min')} hint={t('nt.min.hint')}>
              {(id) => (
                <Select id={id} value={draft.min_severity} onChange={(e) => setDraft({ ...draft, min_severity: e.target.value })}>
                  {SEVERITIES.map((s) => (
                    <option key={s} value={s}>
                      {severityText(t, s)}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </fieldset>
        </section>
        <section className="al-section">
          <h3 className="al-sub">{t('nt.extra')}</h3>
          <fieldset className="plain-fieldset grid-2" disabled={!canEdit}>
            <Field label={t('nt.extra.emails')} hint={t('nt.extra.emails.hint')}>
              {(id) => <Textarea id={id} rows={2} value={draft.extra_emails} onChange={(e) => setDraft({ ...draft, extra_emails: e.target.value })} />}
            </Field>
            <Field label={t('nt.extra.chats')} hint={t('nt.extra.chats.hint')}>
              {(id) => <Textarea id={id} rows={2} value={draft.extra_telegram} onChange={(e) => setDraft({ ...draft, extra_telegram: e.target.value })} />}
            </Field>
            {(draft.teams.enabled || draft.extra_teams !== '') && (
              <Field label={t('nt.extra.teams')} hint={t('nt.extra.teams.hint')}>
                {(id) => <Textarea id={id} rows={2} spellCheck={false} value={draft.extra_teams} onChange={(e) => setDraft({ ...draft, extra_teams: e.target.value })} />}
              </Field>
            )}
            {(draft.zoom.enabled || draft.extra_zoom !== '') && (
              <Field label={t('nt.extra.zoom')} hint={t('nt.extra.zoom.hint')}>
                {(id) => <Textarea id={id} rows={2} spellCheck={false} value={draft.extra_zoom} onChange={(e) => setDraft({ ...draft, extra_zoom: e.target.value })} />}
              </Field>
            )}
          </fieldset>
        </section>
        {Object.keys(defaults).length > 0 && (
          <Templates templates={draft.templates} defaults={defaults} canEdit={canEdit} onChange={(templates) => setDraft({ ...draft, templates })} />
        )}
        {canTest && on && (
          <section className="al-section">
            <h3 className="al-sub">{t('nt.test')}</h3>
            {dirty && <p className="hint">{t('nt.test.saveFirst')}</p>}
            {view.email.enabled && (
              <div className="al-test-row">
                <Input aria-label={t('nt.test.to')} value={testTo.email} placeholder={t('nt.test.to.email')} onChange={(e) => setTestTo({ ...testTo, email: e.target.value })} />
                <Button busy={tester.busy} onClick={() => void test('email')}>
                  <Send size={15} aria-hidden />
                  {t('nt.email')}
                </Button>
              </div>
            )}
            {view.telegram.enabled && (
              <div className="al-test-row">
                <Input aria-label={t('nt.test.to')} value={testTo.telegram} placeholder={t('nt.test.to.chat')} onChange={(e) => setTestTo({ ...testTo, telegram: e.target.value })} />
                <Button busy={tester.busy} onClick={() => void test('telegram')}>
                  <Send size={15} aria-hidden />
                  Telegram
                </Button>
              </div>
            )}
            {(['teams', 'zoom'] as const)
              .filter((c) => (c === 'teams' ? teamsOn : zoomOn))
              .map((c) => (
                <div key={c} className="al-test-row">
                  <Input aria-label={t('nt.test.to')} value={testTo[c]} placeholder={t('nt.test.to.hook')} onChange={(e) => setTestTo({ ...testTo, [c]: e.target.value })} />
                  <Button busy={tester.busy} disabled={!testTo[c].trim()} onClick={() => void test(c)}>
                    <Send size={15} aria-hidden />
                    {c === 'teams' ? 'Teams' : 'Zoom'}
                  </Button>
                </div>
              ))}
            {tester.notice && <Flash kind="ok" title={tester.notice} />}
            {tester.error && (
              <Flash kind="error" title={tester.error.message}>
                {tester.error.detail}
              </Flash>
            )}
          </section>
        )}
      </ProfileCard>
    </>
  )
}

// Templates edits the message templates: a text area for each part of each message, a reset to
// the built-in text, and a preview of a sample incident with the templates as edited.
function Templates({
  templates,
  defaults,
  canEdit,
  onChange,
}: {
  templates: Record<string, string>
  defaults: Record<string, string>
  canEdit: boolean
  onChange: (t: Record<string, string>) => void
}) {
  const t = useT(strings)
  const { locale } = useLocale()
  const previewer = useAction(strings)
  const [preview, setPreview] = useState<NotifyPreview[] | null>(null)
  const show = () =>
    previewer.run(async () => {
      const r = await api<{ messages: NotifyPreview[] }>('POST', '/api/notifications/preview', { templates: overrides(templates, defaults), locale })
      setPreview(r.messages)
    })
  return (
    <section className="al-section">
      <h3 className="al-sub">{t('nt.tpl')}</h3>
      <p className="hint">{t('nt.tpl.hint')}</p>
      {TEMPLATE_MESSAGES.map((m) => {
        const changed = TEMPLATE_PARTS.filter((p) => templates[`${m}.${p}`] !== defaults[`${m}.${p}`]).length
        return (
          <details key={m} className="al-tpl">
            <summary>
              {t(`nt.tpl.${m}`)}
              {changed > 0 && <span className="pill pill-warn">{t('nt.tpl.changed', { n: changed })}</span>}
            </summary>
            <fieldset className="plain-fieldset stack" disabled={!canEdit}>
              {TEMPLATE_PARTS.map((p) => {
                const name = `${m}.${p}`
                const custom = templates[name] !== defaults[name]
                return (
                  <Field key={name} label={t(`nt.tpl.${p}`)} hint={custom ? t('nt.tpl.custom') : undefined}>
                    {(id) => (
                      <div className="stack al-tpl-code">
                        <Textarea
                          id={id}
                          spellCheck={false}
                          rows={p === 'subject' ? 2 : 8}
                          value={templates[name] ?? ''}
                          onChange={(e) => onChange({ ...templates, [name]: e.target.value })}
                        />
                        {custom && canEdit && (
                          <div>
                            <Button type="button" variant="ghost" onClick={() => onChange({ ...templates, [name]: defaults[name] ?? '' })}>
                              <RotateCcw size={14} aria-hidden />
                              {t('nt.tpl.reset')}
                            </Button>
                          </div>
                        )}
                      </div>
                    )}
                  </Field>
                )
              })}
            </fieldset>
          </details>
        )
      })}
      <div>
        <Button type="button" busy={previewer.busy} onClick={() => void show()}>
          <Eye size={15} aria-hidden />
          {t('nt.tpl.preview')}
        </Button>
      </div>
      {previewer.error && (
        <Flash kind="error" title={previewer.error.message}>
          {previewer.error.detail}
        </Flash>
      )}
      {preview &&
        !previewer.error &&
        preview.map((m) => (
          <div key={m.name} className="stack">
            <h4 className="al-sub">{t(`nt.tpl.p.${m.name}`)}</h4>
            {m.error && (
              <Banner kind="error" title={t('nt.tpl.failed')}>
                {m.error}
              </Banner>
            )}
            <p>
              <span className="muted">{t('nt.tpl.subject')}: </span>
              <b>{m.subject}</b>
            </p>
            <pre className="al-tpl-pre" aria-label={t('nt.tpl.text')}>
              {m.text}
            </pre>
            <pre className="al-tpl-pre" aria-label={t('nt.tpl.html')}>
              {m.html}
            </pre>
          </div>
        ))}
    </section>
  )
}

function Reveal({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <AnimatePresence initial={false}>
      {open && (
        <motion.div className="reveal-box" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} transition={reveal}>
          <div className="stack">{children}</div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}
