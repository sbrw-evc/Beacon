import { useState } from 'react'
import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Button, Input } from '../../ui'
import { Chips } from '../services/Badges'
import { strings } from './strings'
import type { CI } from './types'

// Aliases are the other names events call an item by; editable for imported items too, since
// they are kept in Umbrella.
export function Aliases({ ci, editable, onChanged }: { ci: CI; editable: boolean; onChanged: (ci: CI) => void }) {
  const t = useT(strings)
  const list = ci.aliases ?? []
  const [draft, setDraft] = useState<string | null>(null)
  const save = useAction()
  if (draft === null) {
    return (
      <div className="ci-aliases">
        {list.length > 0 ? <Chips items={list.map((x) => ({ key: x, label: x }))} /> : <span className="muted">{t('ci.none')}</span>}
        {editable && (
          <Button variant="ghost" onClick={() => setDraft(list.join(', '))}>
            {t('ci.aliases.edit')}
          </Button>
        )}
        <div className="hint">{t('ci.aliases.hint')}</div>
      </div>
    )
  }
  const submit = () =>
    void save.run(async () => {
      const aliases = draft
        .split(/[,;\n]+/)
        .map((x) => x.trim())
        .filter(Boolean)
      onChanged(await api<CI>('PUT', `/api/cis/${encodeURIComponent(ci.id)}/aliases`, { aliases }))
      setDraft(null)
    })
  return (
    <div className="stack ci-aliases">
      <Input value={draft} placeholder={t('ci.aliases.ph')} aria-label={t('ci.field.aliases')} onChange={(e) => setDraft(e.target.value)} />
      <div className="row">
        <Button variant="primary" busy={save.busy} onClick={submit}>
          {t('ci.aliases.save')}
        </Button>
        <Button
          variant="ghost"
          onClick={() => {
            save.clear()
            setDraft(null)
          }}
        >
          {t('ci.aliases.cancel')}
        </Button>
      </div>
      <ErrorFlash error={save.error} strings={strings} />
    </div>
  )
}
