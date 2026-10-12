import type { ReactNode } from 'react'
import { useLocale, useT } from '../i18n'
import { Banner, Field, formatDate, Input, Password, Select } from '../ui'
import { CheckResult, type Check } from './CheckResult'
import { postgresCheckKey, postgresFailHint, postgresUsable, SSL_MODES, type PostgresDraft, type PostgresReport } from './postgres'
import { postgresStrings } from './postgresStrings'
import { Flash } from '../notify'

export function PostgresForm({ value: pg, onChange }: { value: PostgresDraft; onChange: (d: PostgresDraft) => void }) {
  const t = useT(postgresStrings)
  const set = (patch: Partial<PostgresDraft>) => onChange({ ...pg, ...patch })
  return (
    <div className="stack">
      <div className="grid-3">
        <Field label={t('pg.host')}>
          {(id) => <Input id={id} value={pg.host} placeholder="postgres.example.com" onChange={(e) => set({ host: e.target.value })} spellCheck={false} />}
        </Field>
        <Field label={t('pg.port')}>
          {(id) => <Input id={id} value={pg.port} inputMode="numeric" onChange={(e) => set({ port: e.target.value.replace(/\D/g, '') })} />}
        </Field>
      </div>
      <div className="grid-2">
        <Field label={t('pg.database')}>
          {(id) => <Input id={id} value={pg.database} onChange={(e) => set({ database: e.target.value })} spellCheck={false} />}
        </Field>
        <Field label={t('pg.sslmode')}>
          {(id) => (
            <Select id={id} value={pg.sslmode} onChange={(e) => set({ sslmode: e.target.value })}>
              {SSL_MODES.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>
      <div className="grid-2">
        <Field label={t('pg.user')}>
          {(id) => <Input id={id} value={pg.user} onChange={(e) => set({ user: e.target.value })} autoComplete="off" spellCheck={false} />}
        </Field>
        <Field label={t('pg.password')}>
          {(id) => <Password id={id} value={pg.password} onChange={(e) => set({ password: e.target.value })} autoComplete="new-password" />}
        </Field>
      </div>
    </div>
  )
}

export function PostgresCheckResult({
  check,
  draft,
  container,
  state,
}: {
  check: Check<PostgresReport>
  draft: PostgresDraft
  container?: boolean
  state?: ReactNode
}) {
  const t = useT(postgresStrings)
  const { locale } = useLocale()
  return (
    <CheckResult
      check={check}
      currentKey={postgresCheckKey(draft)}
      failTitle={t('pg.fail')}
      failExtra={() => {
        const hint = postgresFailHint(draft, container)
        return hint && t(hint)
      }}
      ok={(r) => (
        <>
          <Flash kind={postgresUsable(r.probe) ? 'ok' : 'error'} title={postgresUsable(r.probe) ? t('pg.ok') : t('pg.fail')} trigger={r}>
            {t('pg.ok.text', { version: r.probe.version, database: r.probe.database, user: r.probe.user })}
            {!postgresUsable(r.probe) && <p>{t('pg.nocreate')}</p>}
          </Flash>
          {r.probe.has_state && (
            <Banner kind="warn" title={r.probe.saved_at ? t('pg.state', { at: formatDate(r.probe.saved_at, locale) }) : t('pg.state.data')}>
              {state}
            </Banner>
          )}
        </>
      )}
    />
  )
}
