import { isLoopbackUrl } from './loopback'

export type LdapKind = 'ad' | 'openldap'

export type LdapAttr =
  | 'username_attr'
  | 'name_attr'
  | 'last_name_attr'
  | 'first_name_attr'
  | 'middle_name_attr'
  | 'title_attr'
  | 'department_attr'
  | 'manager_attr'
  | 'email_attr'
  | 'photo_attr'

export const LDAP_ATTRS: LdapAttr[] = [
  'username_attr',
  'name_attr',
  'last_name_attr',
  'first_name_attr',
  'middle_name_attr',
  'title_attr',
  'department_attr',
  'manager_attr',
  'email_attr',
  'photo_attr',
]

type KindDefaults = Record<LdapAttr | 'user_filter', string>

export const LDAP_DEFAULTS: Record<LdapKind, KindDefaults> = {
  ad: {
    user_filter: '(&(objectCategory=person)(objectClass=user)(sAMAccountName={username}))',
    username_attr: 'sAMAccountName',
    name_attr: 'displayName',
    email_attr: 'mail',
    first_name_attr: 'givenName',
    last_name_attr: 'sn',
    middle_name_attr: 'middleName',
    title_attr: 'title',
    department_attr: 'department',
    manager_attr: 'manager',
    photo_attr: 'thumbnailPhoto',
  },
  openldap: {
    user_filter: '(&(objectClass=inetOrgPerson)(uid={username}))',
    username_attr: 'uid',
    name_attr: 'cn',
    email_attr: 'mail',
    first_name_attr: 'givenName',
    last_name_attr: 'sn',
    middle_name_attr: '',
    title_attr: 'title',
    department_attr: 'departmentNumber',
    manager_attr: 'manager',
    photo_attr: 'jpegPhoto',
  },
}

export type LdapConfig = KindDefaults & {
  enabled: boolean
  kind: LdapKind
  url: string
  start_tls: boolean
  skip_verify: boolean
  ca_cert: string
  bind_dn: string
  base_dn: string
  admin_group_dn: string
}

export type LdapDraft = LdapConfig & { bind_password: string; test_username: string; test_password: string }

export type LdapIdentity = {
  dn: string
  name: string
  admin: boolean
  title?: string
  department?: string
  manager?: string
  email?: string
  has_photo?: boolean
}

export type LdapReport = {
  ok: boolean
  error?: string
  probe: { server: string; tls: string; base_dn: string; admin_group: boolean; user_authenticated: boolean; user?: LdapIdentity }
}

const TEXT_FIELDS: (keyof LdapConfig)[] = ['url', 'ca_cert', 'bind_dn', 'base_dn', 'admin_group_dn', 'user_filter', ...LDAP_ATTRS]

export function ldapDraft(from: Partial<LdapConfig> = {}): LdapDraft {
  const kind: LdapKind = from.kind === 'openldap' ? 'openldap' : 'ad'
  const draft: LdapDraft = {
    enabled: false,
    kind,
    url: '',
    start_tls: false,
    skip_verify: false,
    ca_cert: '',
    bind_dn: '',
    base_dn: '',
    admin_group_dn: '',
    ...LDAP_DEFAULTS[kind],
    bind_password: '',
    test_username: '',
    test_password: '',
  }
  const configured = Boolean(from.url)
  const saved = configured ? Object.fromEntries(TEXT_FIELDS.map((k) => [k, typeof from[k] === 'string' ? from[k] : ''])) : {}
  return {
    ...draft,
    ...saved,
    enabled: from.enabled === true,
    start_tls: from.start_tls === true,
    skip_verify: from.skip_verify === true,
  }
}

export function ldapConfig(d: LdapDraft): LdapConfig {
  const trimmed = Object.fromEntries(TEXT_FIELDS.map((k) => [k, String(d[k]).trim()]))
  return {
    ...(trimmed as Omit<LdapConfig, 'enabled' | 'kind' | 'start_tls' | 'skip_verify'>),
    enabled: d.enabled,
    kind: d.kind,
    start_tls: d.start_tls,
    skip_verify: d.skip_verify,
  }
}

export function ldapTestBody(d: LdapDraft) {
  return { config: ldapConfig(d), bind_password: d.bind_password, test_username: d.test_username.trim(), test_password: d.test_password }
}

export function ldapCheckKey(d: LdapDraft) {
  return JSON.stringify({ c: ldapConfig(d), p: d.bind_password })
}

export function withKind(d: LdapDraft, kind: LdapKind): LdapDraft {
  const old = LDAP_DEFAULTS[d.kind]
  const fresh = LDAP_DEFAULTS[kind]
  const next = { ...d, kind }
  for (const k of Object.keys(fresh) as (keyof KindDefaults)[]) {
    if (d[k] === old[k] || !d[k]) next[k] = fresh[k]
  }
  return next
}

export function ldapFailHint(error: string, url: string, container?: boolean) {
  if (/Strong Auth Required|Confidentiality Required/i.test(error)) return 'ld.hint.tls'
  if (/certificate|x509/i.test(error)) return 'ld.hint.cert'
  if (container && isLoopbackUrl(url)) return 'ld.hint.loopback'
  return undefined
}

export function ldapComplete(d: LdapDraft, passwordSaved = false) {
  return Boolean(d.url.trim() && d.bind_dn.trim() && d.base_dn.trim() && (passwordSaved || d.bind_password))
}
