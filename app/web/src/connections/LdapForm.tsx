import { Choices } from '../Choice'
import { useT } from '../i18n'
import { Field, Input, Password, Rows, Switch, Textarea } from '../ui'
import { CheckResult, type Check } from './CheckResult'
import { LDAP_ATTRS, ldapCheckKey, ldapFailHint, withKind, type LdapDraft, type LdapKind, type LdapReport } from './ldap'
import { ldapStrings } from './ldapStrings'
import { Flash } from '../notify'

const KINDS: LdapKind[] = ['ad', 'openldap']

export function LdapForm({ value: ld, onChange, passwordHint }: { value: LdapDraft; onChange: (d: LdapDraft) => void; passwordHint?: string }) {
  const t = useT(ldapStrings)
  const set = (patch: Partial<LdapDraft>) => onChange({ ...ld, ...patch })
  return (
    <div className="stack">
      <Field label={t('ld.kind')}>
        {() => (
          <Choices
            label={t('ld.kind')}
            value={ld.kind}
            onChange={(k) => onChange(withKind(ld, k))}
            options={KINDS.map((k) => ({ value: k, title: t(`ld.kind.${k}`) }))}
          />
        )}
      </Field>
      <Field label={t('ld.url')} hint={t('ld.url.hint')}>
        {(id) => <Input id={id} value={ld.url} placeholder="ldaps://dc1.corp.example:636" onChange={(e) => set({ url: e.target.value })} spellCheck={false} />}
      </Field>
      {ld.url.trim().toLowerCase().startsWith('ldap://') && (
        <Switch checked={ld.start_tls} onChange={(v) => set({ start_tls: v })} label={t('ld.starttls')} hint={t('ld.starttls.hint')} />
      )}
      <div className="grid-2">
        <Field label={t('ld.bindDn')} hint={t(`ld.bindDn.hint.${ld.kind}`)}>
          {(id) => <Input id={id} value={ld.bind_dn} onChange={(e) => set({ bind_dn: e.target.value })} autoComplete="off" spellCheck={false} />}
        </Field>
        <Field label={t('ld.bindPassword')} hint={passwordHint}>
          {(id) => <Password id={id} value={ld.bind_password} onChange={(e) => set({ bind_password: e.target.value })} autoComplete="new-password" />}
        </Field>
      </div>
      <Field label={t('ld.baseDn')} hint={t('ld.baseDn.hint')}>
        {(id) => <Input id={id} value={ld.base_dn} onChange={(e) => set({ base_dn: e.target.value })} spellCheck={false} />}
      </Field>
      <Field label={t('ld.filter')} hint={t('ld.filter.hint')}>
        {(id) => <Input id={id} value={ld.user_filter} onChange={(e) => set({ user_filter: e.target.value })} spellCheck={false} />}
      </Field>
      <Field label={t('ld.adminGroup')} hint={t('ld.adminGroup.hint')} optional={t('ld.optional')}>
        {(id) => <Input id={id} value={ld.admin_group_dn} onChange={(e) => set({ admin_group_dn: e.target.value })} spellCheck={false} />}
      </Field>
      <div className="section">
        <div className="section-title">{t('ld.attrs')}</div>
        <p className="hint">{t('ld.attrs.hint')}</p>
        <div className="grid-2">
          {LDAP_ATTRS.map((k) => (
            <Field key={k} label={t(`ld.attr.${k}`)}>
              {(id) => <Input id={id} value={ld[k]} onChange={(e) => set({ [k]: e.target.value })} spellCheck={false} />}
            </Field>
          ))}
        </div>
      </div>
      <div className="section">
        <div className="section-title">{t('ld.tls')}</div>
        <Field label={t('ld.ca')} hint={t('ld.ca.hint')} optional={t('ld.optional')}>
          {(id) => (
            <Textarea
              id={id}
              value={ld.ca_cert}
              onChange={(e) => set({ ca_cert: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              spellCheck={false}
            />
          )}
        </Field>
        <Switch checked={ld.skip_verify} onChange={(v) => set({ skip_verify: v })} label={t('ld.skip')} hint={t('ld.skip.hint')} />
      </div>
      <div className="section">
        <div className="section-title">{t('ld.testUser')}</div>
        <p className="hint">{t('ld.testUser.hint')}</p>
        <div className="grid-2">
          <Field label={t('ld.testUsername')} optional={t('ld.optional')}>
            {(id) => <Input id={id} value={ld.test_username} onChange={(e) => set({ test_username: e.target.value })} autoComplete="off" spellCheck={false} />}
          </Field>
          <Field label={t('ld.testPassword')} optional={t('ld.optional')}>
            {(id) => <Password id={id} value={ld.test_password} onChange={(e) => set({ test_password: e.target.value })} autoComplete="off" />}
          </Field>
        </div>
      </div>
    </div>
  )
}

export function LdapCheckResult({ check, draft, container }: { check: Check<LdapReport>; draft: LdapDraft; container?: boolean }) {
  const t = useT(ldapStrings)
  return (
    <CheckResult
      check={check}
      currentKey={ldapCheckKey(draft)}
      failTitle={t('ld.fail')}
      failExtra={(err) => {
        const hint = ldapFailHint(err, draft.url, container)
        return hint && t(hint)
      }}
      ok={(r) => (
        <Flash kind="ok" title={t('ld.ok')} trigger={r}>
          <p>
            {t('ld.ok.text')} {r.probe.admin_group && t('ld.ok.group')}
          </p>
          {r.probe.user && (
            <>
              <p>
                {t('ld.user', { name: r.probe.user.name, dn: r.probe.user.dn })} {r.probe.user_authenticated && t('ld.user.auth')}{' '}
                {draft.admin_group_dn.trim() && (r.probe.user.admin ? t('ld.user.admin') : t('ld.user.notAdmin'))}
              </p>
              <Rows
                rows={[
                  [t('ld.row.title'), r.probe.user.title],
                  [t('ld.row.department'), r.probe.user.department],
                  [t('ld.row.manager'), r.probe.user.manager],
                  [t('ld.row.email'), r.probe.user.email],
                  [t('ld.photo'), r.probe.user.has_photo ? t('yes') : t('no')],
                ]}
              />
            </>
          )}
        </Flash>
      )}
    />
  )
}
