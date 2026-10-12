import type { ReactNode } from 'react'
import { Choices } from '../Choice'
import { useLocale, useT } from '../i18n'
import { Field, formatDate, Input, Password, Rows, Switch, Textarea } from '../ui'
import { CheckResult, type Check } from './CheckResult'
import { openBaoCheckKey, openBaoFailHint, openBaoMount, type OpenBaoAuth, type OpenBaoDraft, type OpenBaoReport } from './openbao'
import { openBaoStrings } from './openbaoStrings'
import { Flash } from '../notify'

const AUTHS: OpenBaoAuth[] = ['approle', 'token']

export function OpenBaoForm({ value: ob, onChange }: { value: OpenBaoDraft; onChange: (d: OpenBaoDraft) => void }) {
  const t = useT(openBaoStrings)
  const set = (patch: Partial<OpenBaoDraft>) => onChange({ ...ob, ...patch })
  return (
    <div className="stack">
      <div className="grid-3">
        <Field label={t('ob.addr')} hint={t('ob.addr.hint')}>
          {(id) => <Input id={id} value={ob.addr} placeholder="https://openbao:8200" onChange={(e) => set({ addr: e.target.value })} spellCheck={false} />}
        </Field>
        <Field label={t('ob.mount')} hint={t('ob.mount.hint')}>
          {(id) => <Input id={id} value={ob.mount} onChange={(e) => set({ mount: e.target.value })} spellCheck={false} />}
        </Field>
      </div>
      <Field label={t('ob.namespace')} optional={t('ob.optional')}>
        {(id) => <Input id={id} value={ob.namespace} onChange={(e) => set({ namespace: e.target.value })} spellCheck={false} />}
      </Field>
      <Field label={t('ob.auth')}>
        {() => (
          <Choices
            label={t('ob.auth')}
            value={ob.auth}
            onChange={(auth) => set({ auth })}
            options={AUTHS.map((a) => ({ value: a, title: t(`ob.auth.${a}`) }))}
          />
        )}
      </Field>
      {ob.auth === 'token' ? (
        <Field label={t('ob.token')}>{(id) => <Password id={id} value={ob.token} onChange={(e) => set({ token: e.target.value })} autoComplete="off" />}</Field>
      ) : (
        <>
          <div className="grid-2">
            <Field label={t('ob.roleId')}>
              {(id) => <Input id={id} value={ob.role_id} onChange={(e) => set({ role_id: e.target.value })} autoComplete="off" spellCheck={false} />}
            </Field>
            <Field label={t('ob.secretId')}>
              {(id) => <Password id={id} value={ob.secret_id} onChange={(e) => set({ secret_id: e.target.value })} autoComplete="off" />}
            </Field>
          </div>
          <Field label={t('ob.approlePath')}>
            {(id) => <Input id={id} value={ob.approle_path} onChange={(e) => set({ approle_path: e.target.value })} spellCheck={false} />}
          </Field>
        </>
      )}
      <div className="section">
        <div className="section-title">{t('ob.tls')}</div>
        <Field label={t('ob.ca')} hint={t('ob.ca.hint')} optional={t('ob.optional')}>
          {(id) => (
            <Textarea
              id={id}
              value={ob.ca_cert}
              onChange={(e) => set({ ca_cert: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              spellCheck={false}
            />
          )}
        </Field>
        <Switch checked={ob.skip_verify} onChange={(v) => set({ skip_verify: v })} label={t('ob.skip')} hint={t('ob.skip.hint')} />
      </div>
    </div>
  )
}

export function OpenBaoCheckResult<R extends OpenBaoReport>({
  check,
  draft,
  container,
  rows,
  children,
}: {
  check: Check<R>
  draft: OpenBaoDraft
  container?: boolean
  rows?: (r: R) => [ReactNode, ReactNode][]
  children?: (r: R) => ReactNode
}) {
  const t = useT(openBaoStrings)
  const { locale } = useLocale()
  const mount = openBaoMount(draft)
  return (
    <CheckResult
      check={check}
      currentKey={openBaoCheckKey(draft)}
      failTitle={t('ob.fail')}
      failExtra={(err) => {
        const hint = openBaoFailHint(err, draft, container)
        return hint && t(hint, { mount })
      }}
      ok={(r) => (
        <>
          <Flash kind="ok" title={t('ob.ok')} trigger={r}>
            <p>{t('ob.ok.text', { version: r.status.version ?? '?', mount })}</p>
            <Rows
              rows={[[t('ob.policies'), r.status.policies?.join(', ')], [t('ob.expires'), formatDate(r.status.token_expires, locale)], ...(rows?.(r) ?? [])]}
            />
          </Flash>
          {children?.(r)}
        </>
      )}
    />
  )
}
