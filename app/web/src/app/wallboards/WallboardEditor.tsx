import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, Plus, RefreshCw, Search, X } from 'lucide-react'
import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction, useResource } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button, Field, Input, Modal, Segmented, Stepper, Switch, Textarea, TimezoneSelect } from '../../ui'
import { ask } from '../../confirm'
import { notify } from '../../notify'
import { SEVERITIES, SEVERITY_TONE, severityText, METHODS } from '../incidents/types'
import { draftOf, inputOf, MAX_NETWORKS, networkOK, networksOf, opensToAll, publicURL, REFRESH_MAX, REFRESH_MIN, RESOLVED_MAX, type Draft } from './model'
import { strings } from './strings'
import type { BoardLocale, BoardTheme, Ref, Sort, TargetKind, Targets, Wallboard } from './types'

const KINDS: TargetKind[] = ['cis', 'services', 'teams']

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}

function toggle(list: string[], v: string, on: boolean) {
  return on ? (list.includes(v) ? list : [...list, v]) : list.filter((x) => x !== v)
}

export function WallboardEditor({
  value,
  clientIP,
  onClose,
  onSaved,
}: {
  value: Wallboard | 'new' | null
  clientIP: string
  onClose: () => void
  onSaved: () => void
}) {
  const t = useT(strings)
  const [d, setD] = useState<Draft>(() => draftOf(value))
  const [q, setQ] = useState('')
  const save = useAction()
  useEffect(() => {
    setD(draftOf(value))
    setQ('')
    save.clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  const query = useDebounced(q.trim(), 250)
  const found = useResource<Targets>(value && query ? `/api/wallboards/targets?q=${encodeURIComponent(query)}` : '', 0)
  const editing = value && value !== 'new' ? value : null

  const set = (patch: Partial<Draft>) => setD((prev) => ({ ...prev, ...patch }))
  const setTitle = (title: string) => set({ title })
  const add = (kind: TargetKind, r: Ref) => setD((prev) => (prev[kind].some((x) => x.id === r.id) ? prev : { ...prev, [kind]: [...prev[kind], r] }))
  const drop = (kind: TargetKind, id: string) => setD((prev) => ({ ...prev, [kind]: prev[kind].filter((x) => x.id !== id) }))

  const networks = useMemo(() => networksOf(d.networks), [d.networks])
  const bad = networks.filter((n) => !networkOK(n))
  const open = networks.filter((n) => networkOK(n) && opensToAll(n))
  const hasMe = !!clientIP && networks.includes(clientIP)
  const addMe = () => set({ networks: [...networks, clientIP].join('\n') })

  const submit = () =>
    save.run(async () => {
      await api(editing ? 'PUT' : 'POST', editing ? `/api/wallboards/${encodeURIComponent(editing.id)}` : '/api/wallboards', inputOf(d))
      onSaved()
    })

  // rotate gives the board a new random address at once; the old link stops working.
  const rotate = async () => {
    if (!editing || !(await ask({ text: t('wb.rotate.confirm', { title: editing.title }) }))) return
    const out = await save.run(() => api<Wallboard>('POST', `/api/wallboards/${encodeURIComponent(editing.id)}/rotate`))
    if (!out) return
    set({ slug: out.slug })
    notify({ kind: 'ok', title: t('wb.rotated') })
    onSaved()
  }

  const results = found.data && query && q.trim() ? found.data : null
  const resultCount = results ? KINDS.reduce((n, k) => n + (results[k]?.length ?? 0), 0) : 0
  const url = publicURL({ path: '', slug: d.slug || '…' })
  const slugPlaceholder = t('wb.slug.random')

  return (
    <Modal
      open={value !== null}
      title={editing ? t('wb.dialog.edit') : t('wb.dialog.new')}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t('wb.cancel')}</Button>
          <Button variant="primary" busy={save.busy} onClick={() => void submit()}>
            {t('wb.save')}
          </Button>
        </>
      }
    >
      <div className="stack wb-editor">
        <section className="wb-section">
          <div className="section-title">{t('wb.section.general')}</div>
          <Field label={t('wb.title')}>{(id) => <Input id={id} value={d.title} maxLength={200} placeholder={t('wb.title.ph')} onChange={(e) => setTitle(e.target.value)} />}</Field>
          <Field label={t('wb.slug')} hint={t('wb.slug.hint', { url })}>
            {(id) => (
              <Input
                id={id}
                value={d.slug}
                maxLength={63}
                spellCheck={false}
                autoCapitalize="off"
                className="wb-mono"
                placeholder={slugPlaceholder}
                onChange={(e) => set({ slug: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''), slugTouched: true })}
              />
            )}
          </Field>
          {editing && (
            <div className="row wb-access-row">
              <Button variant="secondary" busy={save.busy} onClick={() => void rotate()}>
                <RefreshCw size={15} aria-hidden />
                {t('wb.rotate')}
              </Button>
              <span className="hint muted">{t('wb.rotate.hint')}</span>
            </div>
          )}
          <Field label={t('wb.description')} hint={t('wb.description.hint')}>
            {(id) => <Textarea id={id} rows={2} maxLength={2000} value={d.description} onChange={(e) => set({ description: e.target.value })} />}
          </Field>
          <Switch checked={d.enabled} onChange={(enabled) => set({ enabled })} label={t('wb.enabled')} hint={t('wb.enabled.hint')} />
        </section>

        <section className="wb-section">
          <div className="section-title">{t('wb.section.filters')}</div>
          <Field label={t('wb.targets')} hint={t('wb.targets.hint')}>
            {(id) => (
              <div className="mw-search">
                <Search size={16} className="mw-search-icon" aria-hidden />
                <Input id={id} value={q} placeholder={t('wb.search')} onChange={(e) => setQ(e.target.value)} />
              </div>
            )}
          </Field>
          {results && (
            <div className="mw-results">
              {resultCount === 0 && <p className="muted">{t('wb.nothing')}</p>}
              {KINDS.map(
                (kind) =>
                  (results[kind]?.length ?? 0) > 0 && (
                    <div key={kind}>
                      <div className="mw-results-head">{t(`wb.found.${kind}`)}</div>
                      {results[kind].map((r) => (
                        <button key={r.id} type="button" className="mw-result" disabled={d[kind].some((x) => x.id === r.id)} onClick={() => add(kind, r)}>
                          <Plus size={14} aria-hidden /> {r.name}
                        </button>
                      ))}
                    </div>
                  ),
              )}
            </div>
          )}
          {KINDS.some((k) => d[k].length > 0) && (
            <div className="wb-chosen">
              {KINDS.map(
                (kind) =>
                  d[kind].length > 0 && (
                    <div key={kind} className="wb-chosen-row">
                      <span className="hint muted wb-chosen-label">{t(`wb.chosen.${kind}`)}</span>
                      <span className="mw-chosen">
                        {d[kind].map((r) => (
                          <span key={r.id} className={`mw-tag wb-tag-${kind}`}>
                            {r.name}
                            {r.missing && <span className="muted"> ({t('wb.missing')})</span>}
                            <button type="button" aria-label={t('wb.remove', { name: r.name })} onClick={() => drop(kind, r.id)}>
                              <X size={13} />
                            </button>
                          </span>
                        ))}
                      </span>
                    </div>
                  ),
              )}
            </div>
          )}
          <div className="wb-checks-grid">
            <fieldset className="wb-checks">
              <legend>{t('wb.severities')}</legend>
              {SEVERITIES.map((s) => (
                <label key={s} className="wb-check">
                  <input type="checkbox" checked={d.severities.includes(s)} onChange={(e) => set({ severities: toggle(d.severities, s, e.target.checked) })} />
                  <span className={`wb-sev-dot wb-sev-${SEVERITY_TONE[s]}`} aria-hidden />
                  {severityText(t, s)}
                </label>
              ))}
              <div className="hint muted">{t('wb.severities.hint')}</div>
            </fieldset>
            <fieldset className="wb-checks">
              <legend>{t('wb.methods')}</legend>
              {METHODS.map((m) => (
                <label key={m} className="wb-check">
                  <input type="checkbox" checked={d.methods.includes(m)} onChange={(e) => set({ methods: toggle(d.methods, m, e.target.checked) })} />
                  {t(`wb.method.${m}`)}
                </label>
              ))}
              <div className="hint muted">{t('wb.methods.hint')}</div>
            </fieldset>
          </div>
          <Switch
            checked={d.show_acknowledged}
            onChange={(show_acknowledged) => set({ show_acknowledged })}
            label={t('wb.show_acknowledged')}
            hint={t('wb.show_acknowledged.hint')}
          />
          <Switch checked={d.show_suppressed} onChange={(show_suppressed) => set({ show_suppressed })} label={t('wb.show_suppressed')} />
          <div className="wb-inline">
            <div className="wb-inline-text">
              <span>{t('wb.resolved')}</span>
              <span className="hint muted">{t('wb.resolved.hint')}</span>
            </div>
            <Stepper
              value={d.resolved_minutes}
              min={0}
              max={RESOLVED_MAX}
              label={t('wb.resolved')}
              suffix={t('wb.minutes')}
              onChange={(resolved_minutes) => set({ resolved_minutes })}
            />
          </div>
        </section>

        <section className="wb-section">
          <div className="section-title">{t('wb.section.display')}</div>
          <div className="wb-display">
            <Field label={t('wb.sort')}>
              {() => (
                <Segmented<Sort>
                  value={d.sort}
                  label={t('wb.sort')}
                  options={[
                    { value: 'newest', label: t('wb.sort.newest') },
                    { value: 'oldest', label: t('wb.sort.oldest') },
                  ]}
                  onChange={(sort) => set({ sort })}
                />
              )}
            </Field>
            <Field label={t('wb.refresh')}>
              {(id) => (
                <Stepper
                  id={id}
                  value={d.refresh_seconds}
                  min={REFRESH_MIN}
                  max={REFRESH_MAX}
                  label={t('wb.refresh')}
                  suffix={t('wb.seconds')}
                  onChange={(refresh_seconds) => set({ refresh_seconds })}
                />
              )}
            </Field>
            <Field label={t('wb.theme')}>
              {() => (
                <Segmented<BoardTheme>
                  value={d.theme}
                  label={t('wb.theme')}
                  options={[
                    { value: 'dark', label: t('wb.theme.dark') },
                    { value: 'light', label: t('wb.theme.light') },
                  ]}
                  onChange={(theme) => set({ theme })}
                />
              )}
            </Field>
            <Field label={t('wb.locale')}>
              {() => (
                <Segmented<BoardLocale | 'auto'>
                  value={d.locale || 'auto'}
                  label={t('wb.locale')}
                  options={[
                    { value: 'auto', label: t('wb.locale.auto') },
                    { value: 'ru', label: t('wb.locale.ru') },
                    { value: 'en', label: t('wb.locale.en') },
                  ]}
                  onChange={(v) => set({ locale: v === 'auto' ? '' : v })}
                />
              )}
            </Field>
            <Field label={t('wb.timezone')}>
              {(id) => <TimezoneSelect id={id} value={d.timezone} defaultLabel={t('wb.timezone.auto')} onChange={(timezone) => set({ timezone })} />}
            </Field>
          </div>
        </section>

        <section className="wb-section">
          <div className="section-title">{t('wb.section.access')}</div>
          <Field label={t('wb.allowed')} hint={t('wb.allowed.hint')}>
            {(id) => (
              <Textarea
                id={id}
                rows={4}
                spellCheck={false}
                className="wb-mono"
                placeholder={'10.20.0.0/24\n10.20.5.15'}
                value={d.networks}
                onChange={(e) => set({ networks: e.target.value })}
              />
            )}
          </Field>
          <div className="row wb-access-row">
            {clientIP && (
              <Button variant="secondary" disabled={hasMe || networks.length >= MAX_NETWORKS} onClick={addMe}>
                <Plus size={16} aria-hidden />
                {t('wb.allowed.add_me', { ip: clientIP })}
              </Button>
            )}
            {clientIP && <span className="hint muted">{t('wb.allowed.proxy', { ip: clientIP })}</span>}
          </div>
          {bad.length > 0 && (
            <p className="wb-warn-line wb-bad" role="status">
              <AlertTriangle size={15} aria-hidden />
              {t('wb.allowed.bad', { list: bad.join(', ') })}
            </p>
          )}
          {open.length > 0 && <Banner kind="warn" title={t('wb.allowed.everyone', { net: open.join(', ') })} />}
        </section>

        <ErrorFlash error={save.error} strings={strings} />
      </div>
    </Modal>
  )
}
