import { checkPassword, type PasswordPolicy } from '../../policy'

const LATIN_UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
const LATIN_LOWER = 'abcdefghijkmnopqrstuvwxyz'
const CYRILLIC_UPPER = 'АБВГДЕЖИКЛМНПРСТУФХЦЧШЭЮЯ'
const CYRILLIC_LOWER = 'абвгдежиклмнпрстуфхцчшэюя'
const DIGITS = '23456789'
const SPECIAL = '!@#$%^&*-_=+?'
const MIN_GENERATED = 16

function pick(chars: string) {
  const list = [...chars]
  const n = new Uint32Array(1)
  crypto.getRandomValues(n)
  return list[n[0] % list.length]
}

function shuffle(chars: string[]) {
  const out = [...chars]
  for (let i = out.length - 1; i > 0; i--) {
    const n = new Uint32Array(1)
    crypto.getRandomValues(n)
    const j = n[0] % (i + 1)
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}

export function generatePassword(policy: PasswordPolicy, username: string): string {
  const cyrillic = policy.letters === 'cyrillic'
  const upper = cyrillic ? CYRILLIC_UPPER : LATIN_UPPER
  const lower = cyrillic ? CYRILLIC_LOWER : LATIN_LOWER
  const length = Math.max(policy.min_length, MIN_GENERATED)
  for (;;) {
    const chars: string[] = [pick(upper), pick(lower)]
    for (let i = 0; i < (policy.require_digits ? policy.min_digits : 1); i++) chars.push(pick(DIGITS))
    for (let i = 0; i < (policy.require_special ? policy.min_special : 1); i++) chars.push(pick(SPECIAL))
    const all = upper + lower + DIGITS + SPECIAL
    while (chars.length < length) chars.push(pick(all))
    const password = shuffle(chars).join('')
    if (checkPassword(password, username, policy).length === 0) return password
  }
}
