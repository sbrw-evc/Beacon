type T = (key: string) => string

const UNITS = ['unit.b', 'unit.kb', 'unit.mb', 'unit.gb', 'unit.tb']

function tag(locale: string) {
  return locale === 'ru' ? 'ru-RU' : 'en-GB'
}

export function formatBytes(n: number, locale: string, t: T) {
  let v = n
  let i = 0
  while (Math.abs(v) >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toLocaleString(tag(locale), { maximumFractionDigits: i === 0 ? 0 : 1 })} ${t(UNITS[i])}`
}

export function formatCount(n: number, locale: string) {
  return n.toLocaleString(tag(locale), { maximumFractionDigits: 1 })
}

export function formatPercent(part: number, total: number, locale: string) {
  if (total <= 0) return '—'
  return (part / total).toLocaleString(tag(locale), { style: 'percent', maximumFractionDigits: 2 })
}

export function formatMs(ms: number, locale: string, t: T) {
  if (ms < 1000) return `${ms.toLocaleString(tag(locale), { maximumFractionDigits: ms < 10 ? 2 : 0 })} ${t('unit.ms')}`
  const s = Math.floor(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return `${h} ${t('unit.h')} ${m} ${t('unit.min')}`
  if (m > 0) return `${m} ${t('unit.min')} ${s % 60} ${t('unit.s')}`
  return `${(ms / 1000).toLocaleString(tag(locale), { maximumFractionDigits: 1 })} ${t('unit.s')}`
}
