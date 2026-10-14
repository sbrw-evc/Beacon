import { isCommonPassword } from './commonPasswords'

export type Letters = 'latin' | 'cyrillic' | 'latin_cyrillic' | 'any'

export type PasswordPolicy = {
  min_length: number
  require_digits: boolean
  min_digits: number
  require_special: boolean
  min_special: number
  require_mixed_case: boolean
  letters: Letters
  max_age_days: number
  warn_days: number
  history_count: number
  block_common: boolean
  lockout_attempts: number
  lockout_minutes: number
}

export type Violation = 'length' | 'digits' | 'special' | 'mixed_case' | 'letters' | 'username' | 'control' | 'common' | 'history'

export const defaultPolicy: PasswordPolicy = {
  min_length: 12,
  require_digits: true,
  min_digits: 1,
  require_special: true,
  min_special: 1,
  require_mixed_case: true,
  letters: 'latin',
  max_age_days: 0,
  warn_days: 0,
  history_count: 5,
  block_common: true,
  lockout_attempts: 10,
  lockout_minutes: 15,
}

export const MAX_AGE_DAYS = 3650
export const MAX_WARN_DAYS = 90
export const MAX_HISTORY = 24
export const MIN_LOCKOUT = 3
export const MAX_LOCKOUT = 100
export const MAX_LOCKOUT_MINUTES = 1440

const LETTER = /\p{L}/u
const DIGIT = /\p{Nd}/u
const SPECIAL = /[\p{P}\p{S}]/u
const CONTROL = /\p{Cc}/u
const UPPER = /\p{Lu}/u
const LOWER = /\p{Ll}/u
const SCRIPT: Record<Letters, RegExp | null> = {
  latin: /\p{Script=Latin}/u,
  cyrillic: /\p{Script=Cyrillic}/u,
  latin_cyrillic: /[\p{Script=Latin}\p{Script=Cyrillic}]/u,
  any: null,
}

export type PolicyProblem = 'length' | 'digits' | 'special' | 'fit' | 'max_age' | 'warn' | 'history' | 'lockout'

export function policyError(p: PasswordPolicy): PolicyProblem | null {
  if (!Number.isInteger(p.min_length) || p.min_length < 8 || p.min_length > 128) return 'length'
  if (p.require_digits && (!Number.isInteger(p.min_digits) || p.min_digits < 1 || p.min_digits > 16)) return 'digits'
  if (p.require_special && (!Number.isInteger(p.min_special) || p.min_special < 1 || p.min_special > 16)) return 'special'
  const need = (p.require_digits ? p.min_digits : 0) + (p.require_special ? p.min_special : 0) + (p.require_mixed_case ? 2 : 0)
  if (need > p.min_length) return 'fit'
  if (!Number.isInteger(p.max_age_days) || p.max_age_days < 0 || p.max_age_days > MAX_AGE_DAYS) return 'max_age'
  if (p.max_age_days > 0 && (!Number.isInteger(p.warn_days) || p.warn_days < 0 || p.warn_days > MAX_WARN_DAYS || p.warn_days >= p.max_age_days)) return 'warn'
  const history = p.history_count ?? 0
  if (!Number.isInteger(history) || history < 0 || history > MAX_HISTORY) return 'history'
  const attempts = p.lockout_attempts ?? 0
  if (!Number.isInteger(attempts) || (attempts !== 0 && (attempts < MIN_LOCKOUT || attempts > MAX_LOCKOUT))) return 'lockout'
  if (attempts > 0 && (!Number.isInteger(p.lockout_minutes) || p.lockout_minutes < 1 || p.lockout_minutes > MAX_LOCKOUT_MINUTES)) return 'lockout'
  return null
}

export function checkPassword(password: string, username: string, p: PasswordPolicy): Violation[] {
  const out = new Set<Violation>()
  const chars = [...password]
  if (chars.length < p.min_length || new TextEncoder().encode(password).length > 256) out.add('length')
  let digits = 0
  let special = 0
  let upper = false
  let lower = false
  for (const ch of chars) {
    if (CONTROL.test(ch)) out.add('control')
    else if (DIGIT.test(ch)) digits++
    else if (LETTER.test(ch)) {
      const allowed = SCRIPT[p.letters]
      if (allowed && !allowed.test(ch)) out.add('letters')
      upper ||= UPPER.test(ch)
      lower ||= LOWER.test(ch)
    } else if (SPECIAL.test(ch)) special++
  }
  if (p.require_digits && digits < p.min_digits) out.add('digits')
  if (p.require_special && special < p.min_special) out.add('special')
  if (p.require_mixed_case && (!upper || !lower)) out.add('mixed_case')
  if (username && password.toLowerCase() === username.toLowerCase()) out.add('username')
  if (p.block_common && isCommonPassword(password, username)) out.add('common')
  return [...out]
}

export function rules(p: PasswordPolicy): Violation[] {
  const r: Violation[] = ['length']
  if (p.require_digits) r.push('digits')
  if (p.require_special) r.push('special')
  if (p.require_mixed_case) r.push('mixed_case')
  if (p.letters !== 'any') r.push('letters')
  r.push('username')
  if (p.block_common) r.push('common')
  return r
}

export const policyStrings = {
  en: {
    'pp.length': 'At least {n} characters',
    'pp.digits': 'At least {n} digits',
    'pp.special': 'At least {n} special characters (! @ # - _ …)',
    'pp.mixed_case': 'Upper and lower case letters',
    'pp.letters': 'Letters: {letters}',
    'pp.username': 'Differs from the login',
    'pp.control': 'No control characters',
    'pp.common': 'Not a common password, sequence or the login with a few characters',
    'pp.history': 'Differs from the last {n} passwords',
    'pp.match': 'Passwords match',
    'pp.letters.latin': 'Latin only',
    'pp.letters.cyrillic': 'Cyrillic only',
    'pp.letters.latin_cyrillic': 'Latin and Cyrillic',
    'pp.letters.any': 'any alphabet',
  },
  ru: {
    'pp.length': 'Не короче {n} символов',
    'pp.digits': 'Не меньше {n} цифр',
    'pp.special': 'Не меньше {n} спецсимволов (! @ # - _ …)',
    'pp.mixed_case': 'Заглавные и строчные буквы',
    'pp.letters': 'Буквы: {letters}',
    'pp.username': 'Не совпадает с логином',
    'pp.control': 'Без управляющих символов',
    'pp.common': 'Не распространённый пароль, не последовательность и не логин с парой символов',
    'pp.history': 'Не совпадает с {n} последними паролями',
    'pp.match': 'Пароли совпадают',
    'pp.letters.latin': 'только латиница',
    'pp.letters.cyrillic': 'только кириллица',
    'pp.letters.latin_cyrillic': 'латиница и кириллица',
    'pp.letters.any': 'любой алфавит',
  },
}

export function ruleText(t: (k: string, v?: Record<string, string | number>) => string, rule: Violation, p: PasswordPolicy) {
  const n = rule === 'length' ? p.min_length : rule === 'digits' ? p.min_digits : rule === 'special' ? p.min_special : rule === 'history' ? p.history_count : 0
  return t(`pp.${rule}`, { n, letters: t(`pp.letters.${p.letters}`) })
}

type Translate = (k: string, v?: Record<string, string | number>) => string

export function expiryText(t: Translate, p: PasswordPolicy) {
  if (!p.max_age_days) return t('pol.maxAge.never')
  const days = t('pol.maxAge.days', { n: p.max_age_days })
  return p.warn_days ? `${days}, ${t('pol.maxAge.warn', { n: p.warn_days })}` : days
}

export function policyRows(t: Translate, p: PasswordPolicy): [string, string][] {
  const content = rules(p)
    .filter((r) => r !== 'username')
    .map((r): [string, string] => [t(`pol.row.${r}`), ruleText(t, r, p)])
  const rows: [string, string][] = [...content, [t('pol.row.max_age'), expiryText(t, p)]]
  if (p.history_count > 0) rows.push([t('pol.row.history'), t('pol.history.n', { n: p.history_count })])
  rows.push([t('pol.row.lockout'), lockoutText(t, p)])
  return rows
}

export function lockoutText(t: Translate, p: PasswordPolicy) {
  return p.lockout_attempts ? t('pol.lockout.text', { n: p.lockout_attempts, m: p.lockout_minutes }) : t('pol.lockout.off')
}
