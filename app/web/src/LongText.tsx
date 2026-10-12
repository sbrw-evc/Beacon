import { useState } from 'react'
import { ChevronDown, ChevronUp, Copy, ExternalLink } from 'lucide-react'
import { useT, type Dict } from './i18n'

const strings: Dict = {
  en: {
    'lt.more': 'Show all ({lines} lines)',
    'lt.more.chars': 'Show all',
    'lt.less': 'Collapse',
    'lt.copy': 'Copy',
    'lt.copied': 'Copied',
  },
  ru: {
    'lt.more': 'Показать полностью ({lines} строк)',
    'lt.more.chars': 'Показать полностью',
    'lt.less': 'Свернуть',
    'lt.copy': 'Копировать',
    'lt.copied': 'Скопировано',
  },
}

// LongText shows a text of any length (a stack trace, a long description) collapsed to its first
// lines; the rest opens on demand and the whole text can be copied.
export function LongText({ text, lines = 8, chars = 1200 }: { text: string; lines?: number; chars?: number }) {
  const t = useT(strings)
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const all = text.split('\n')
  const long = all.length > lines || text.length > chars
  let shown = text
  if (long && !open) {
    shown = all.slice(0, lines).join('\n')
    if (shown.length > chars) shown = shown.slice(0, chars)
    shown += ' …'
  }
  const copy = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    })
  }
  return (
    <div className={`long-text ${open ? 'open' : ''}`}>
      <pre>{shown}</pre>
      <div className="long-text-actions">
        {long && (
          <button type="button" className="link-button" aria-expanded={open} onClick={() => setOpen(!open)}>
            {open ? <ChevronUp size={14} aria-hidden /> : <ChevronDown size={14} aria-hidden />}
            {open ? t('lt.less') : all.length > lines ? t('lt.more', { lines: all.length }) : t('lt.more.chars')}
          </button>
        )}
        <button type="button" className="link-button" onClick={copy}>
          <Copy size={14} aria-hidden />
          {copied ? t('lt.copied') : t('lt.copy')}
        </button>
      </div>
    </div>
  )
}

const isURL = (v: string) => /^https?:\/\/\S+$/i.test(v.trim())

// FieldList shows the named values a connector puts into an incident; addresses are links and
// long values fold like LongText.
export function FieldList({ fields }: { fields: { name: string; value: string }[] }) {
  return (
    <dl className="field-list">
      {fields.map((f, i) => (
        <div key={f.name + i}>
          <dt>{f.name}</dt>
          <dd>
            {isURL(f.value) ? (
              <a href={f.value.trim()} target="_blank" rel="noopener noreferrer">
                {f.value.trim()}
                <ExternalLink size={13} aria-hidden />
              </a>
            ) : f.value.includes('\n') || f.value.length > 200 ? (
              <LongText text={f.value} lines={3} chars={200} />
            ) : (
              f.value
            )}
          </dd>
        </div>
      ))}
    </dl>
  )
}
