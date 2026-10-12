import { useState, type KeyboardEvent } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useT } from '../../i18n'
import { Button, Input, Select } from '../../ui'
import { Chips, type ChipItem } from './Badges'
import { strings } from './strings'
import type { TeamOption } from './teams'
import type { Link } from './types'

export type Option = { id: string; label: string; depth?: number }

export function teamOptions(options: TeamOption[]): Option[] {
  return options.map((o) => ({ id: o.id, label: o.label, depth: o.depth }))
}

function optionText(o: Option) {
  return `${' '.repeat(o.depth ?? 0)}${o.label}`
}

export function OptionSelect({
  id,
  value,
  options,
  placeholder,
  onChange,
  disabled,
}: {
  id?: string
  value: string
  options: Option[]
  placeholder: string
  onChange: (id: string) => void
  disabled?: boolean
}) {
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled}>
      <option value="">{placeholder}</option>
      {options.map((o) => (
        <option key={o.id} value={o.id}>
          {optionText(o)}
        </option>
      ))}
    </Select>
  )
}

export function MultiPicker({
  id,
  selected,
  options,
  labelOf,
  placeholder,
  onChange,
}: {
  id?: string
  selected: string[]
  options: Option[]
  labelOf: (id: string) => ChipItem
  placeholder: string
  onChange: (ids: string[]) => void
}) {
  const t = useT(strings)
  const free = options.filter((o) => !selected.includes(o.id))
  const items = selected.map(labelOf)
  const name = (key: string) => String(items.find((i) => i.key === key)?.title ?? key)
  return (
    <div className="svc-picker">
      <Chips items={items} onRemove={(key) => onChange(selected.filter((x) => x !== key))} removeLabel={(key) => t('svc.remove', { name: name(key) })} />
      {free.length > 0 && <OptionSelect id={id} value="" options={free} placeholder={placeholder} onChange={(v) => v && onChange([...selected, v])} />}
    </div>
  )
}

export function TagInput({ id, value, suggestions, onChange }: { id?: string; value: string[]; suggestions: string[]; onChange: (tags: string[]) => void }) {
  const t = useT(strings)
  const [draft, setDraft] = useState('')
  const listId = `${id ?? 'svc-tags'}-list`
  const commit = (text: string) => {
    const next = text
      .split(/[\s,]+/)
      .map((x) => x.trim().toLowerCase())
      .filter((x) => x && !value.includes(x))
    if (next.length) onChange([...value, ...new Set(next)])
    setDraft('')
  }
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      commit(draft)
    } else if (e.key === 'Backspace' && draft === '' && value.length) {
      onChange(value.slice(0, -1))
    }
  }
  return (
    <div className="svc-picker">
      <Chips
        items={value.map((tag) => ({ key: tag, label: `#${tag}`, title: tag }))}
        onRemove={(tag) => onChange(value.filter((x) => x !== tag))}
        removeLabel={(tag) => t('svc.remove', { name: tag })}
      />
      <Input
        id={id}
        value={draft}
        list={listId}
        placeholder={t('svc.field.tags.placeholder')}
        onChange={(e) => (/[\s,]$/.test(e.target.value) ? commit(e.target.value) : setDraft(e.target.value))}
        onKeyDown={onKey}
        onBlur={() => draft && commit(draft)}
      />
      <datalist id={listId}>
        {suggestions
          .filter((s) => !value.includes(s))
          .map((s) => (
            <option key={s} value={s} />
          ))}
      </datalist>
    </div>
  )
}

type Row = Link & { key: number }

export function LinksEditor({ value, onChange }: { value: Link[]; onChange: (links: Link[]) => void }) {
  const t = useT(strings)
  const [keys, setKeys] = useState(() => value.map((_, i) => i))
  const [next, setNext] = useState(value.length)
  const rows: Row[] = value.map((l, i) => ({ ...l, key: keys[i] ?? -i - 1 }))
  const set = (i: number, patch: Partial<Link>) => onChange(value.map((l, j) => (j === i ? { ...l, ...patch } : l)))
  const add = () => {
    setKeys([...keys, next])
    setNext(next + 1)
    onChange([...value, { title: '', url: '' }])
  }
  const remove = (i: number) => {
    setKeys(keys.filter((_, j) => j !== i))
    onChange(value.filter((_, j) => j !== i))
  }
  return (
    <div className="svc-links">
      <AnimatePresence initial={false}>
        {rows.map((l, i) => (
          <motion.div
            key={l.key}
            className="svc-link-row"
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
          >
            <Input
              value={l.title}
              placeholder={t('svc.field.link.title')}
              aria-label={t('svc.field.link.title')}
              onChange={(e) => set(i, { title: e.target.value })}
            />
            <Input
              value={l.url}
              type="url"
              inputMode="url"
              placeholder="https://"
              aria-label={t('svc.field.link.url')}
              onChange={(e) => set(i, { url: e.target.value })}
            />
            <button type="button" className="icon-btn" onClick={() => remove(i)} aria-label={t('svc.remove', { name: l.title || l.url || '' })}>
              <Trash2 size={16} />
            </button>
          </motion.div>
        ))}
      </AnimatePresence>
      <div>
        <Button type="button" variant="ghost" onClick={add} disabled={value.length >= 20}>
          <Plus size={16} />
          {t('svc.field.link.add')}
        </Button>
      </div>
    </div>
  )
}
