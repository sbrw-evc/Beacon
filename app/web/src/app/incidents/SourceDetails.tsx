import { FieldList, LongText } from '../../LongText'
import { useT } from '../../i18n'
import { strings } from './strings'
import type { Detail, Source } from './types'

const hasDetails = (s: Source) => !!s.description || (s.fields?.length ?? 0) > 0

// IncidentDetails is what the connector made of the alert: its fields and its full text,
// collapsed to the first lines. It shows the newest firing source that has them, else the newest.
export function IncidentDetails({ d }: { d: Detail }) {
  const t = useT(strings)
  const list = Object.values(d.alert.sources)
    .filter(hasDetails)
    .sort((x, y) => (x.status === y.status ? y.last_seen.localeCompare(x.last_seen) : x.status === 'firing' ? -1 : 1))
  const s = list[0]
  if (!s) return null
  return (
    <section className="inc-details">
      <h3>
        {t('inc.details')}
        {Object.keys(d.alert.sources).length > 1 && <span className="muted"> · {d.connectors[s.connector_id] ?? s.connector_id}</span>}
      </h3>
      <SourceExtra s={s} />
    </section>
  )
}

export function SourceExtra({ s }: { s: Source }) {
  return (
    <div className="stack inc-source-extra">
      {(s.fields?.length ?? 0) > 0 && <FieldList fields={s.fields ?? []} />}
      {s.description && <LongText text={s.description} />}
    </div>
  )
}

// SourceDetailsToggle opens the fields and the text of one source in the list of sources.
export function SourceDetailsToggle({ s }: { s: Source }) {
  const t = useT(strings)
  if (!hasDetails(s)) return null
  return (
    <details className="inc-source-details">
      <summary>{t('inc.details.show')}</summary>
      <SourceExtra s={s} />
    </details>
  )
}
