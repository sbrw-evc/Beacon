/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest'
import type { Dict } from '../i18n'

// Every dictionary of the web app: each module exporting a Dict ({ en, ru }).
const modules = import.meta.glob<Record<string, unknown>>(['../**/*trings.ts', '../i18n.tsx', '../notify.tsx', '../ui.tsx', '../LongText.tsx', '../confirm.tsx', '../connections/CheckResult.tsx'], { eager: true })
const sources = import.meta.glob<string>(['../**/*.ts', '../**/*.tsx', '!../test/**', '!../**/*.test.ts'], { eager: true, query: '?raw', import: 'default' })

function isDict(v: unknown): v is Dict {
  return !!v && typeof v === 'object' && 'en' in v && 'ru' in v && typeof (v as Dict).en === 'object' && typeof (v as Dict).ru === 'object'
}

const dicts: [string, Dict][] = Object.entries(modules).flatMap(([file, mod]) =>
  Object.entries(mod)
    .filter(([, v]) => isDict(v))
    .map(([name, v]) => [`${file}#${name}`, v as Dict] as [string, Dict]),
)

// {name} placeholders; ${path} is template syntax shown to the user, not a placeholder.
const placeholders = (s: string) => [...s.matchAll(/(?<!\$)\{(\w+)\}/g)].map((m) => m[1]).sort()

describe('translations', () => {
  it('finds the dictionaries', () => {
    expect(dicts.length).toBeGreaterThan(30)
  })

  for (const [name, d] of dicts) {
    it(`${name}: English and Russian have the same keys and placeholders`, () => {
      const en = Object.keys(d.en).sort()
      const ru = Object.keys(d.ru).sort()
      expect(en.filter((k) => !(k in d.ru)), 'missing in Russian').toEqual([])
      // Russian has plural forms English has not: key.few and key.many next to key.one / key.other.
      expect(ru.filter((k) => !(k in d.en) && !/\.(few|many)$/.test(k)), 'missing in English').toEqual([])
      for (const k of en.filter((x) => x in d.ru)) {
        expect(d.ru[k].trim(), `empty Russian text for ${k}`).not.toBe('')
        expect(placeholders(d.ru[k]), `placeholders of ${k}`).toEqual(placeholders(d.en[k]))
      }
    })
  }

  it('every literal key the code asks for has a text', () => {
    const known = new Set(dicts.flatMap(([, d]) => Object.keys(d.en)))
    const missing: string[] = []
    for (const [file, src] of Object.entries(sources)) {
      for (const m of src.matchAll(/\bt\(\s*'([a-z][\w.-]*)'/g)) {
        if (!known.has(m[1])) missing.push(`${file}: ${m[1]}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('nothing left of the sign-in Umbrella had before SPA', () => {
    const words = /passkey|password_policy|\/api\/auth\/login|webauthn|entra_enabled/i
    const hits = Object.entries(sources)
      .filter(([, src]) => words.test(src))
      .map(([file]) => file)
    expect(hits).toEqual([])
  })
})
