import { useState } from 'react'
import { useT } from '../../i18n'
import { Banner, Switch } from '../../ui'
import { mergeDicts } from '../../connections/connectionStrings'
import { strings as ciStrings } from '../cis/strings'
import { strings as monStrings } from '../monitoring/strings'
import { strings as svcStrings } from '../services/strings'
import { strings } from './strings'
import './bulk.css'

export type BulkSummary = Record<string, number>

export type BulkCIItem = { service_id: string; service_name: string; ci_id: string; ci_name: string; result: string; error?: string; detail?: string }
export type BulkCIsResult = { items: BulkCIItem[]; summary: BulkSummary }

export type BulkHostItem = {
  source_id: string
  source_name: string
  key: string
  host: string
  result: string
  ci?: { id: string; name: string }
  error?: string
  detail?: string
}
export type BulkHostsResult = { items: BulkHostItem[]; summary: BulkSummary }

export type BulkRow = { key: string; cells: string[]; result: string; error?: string; detail?: string; note?: string }

// The errors of an item come from wherever the single action would have reported them.
const all = mergeDicts(ciStrings, svcStrings, monStrings, strings)

const DONE = new Set(['bound', 'unbound', 'created', 'linked'])
const PROBLEM = new Set(['failed', 'skipped'])

const tone = (result: string) => (result === 'failed' ? 'pill-error' : result === 'skipped' ? 'pill-warn' : DONE.has(result) ? 'pill-ok' : 'pill-off')

export function problems(summary: BulkSummary) {
  return (summary.failed ?? 0) + (summary.skipped ?? 0)
}

export function ciRows(r: BulkCIsResult): BulkRow[] {
  return r.items.map((it) => ({ key: `${it.service_id}/${it.ci_id}`, cells: [it.ci_name, it.service_name], result: it.result, error: it.error, detail: it.detail }))
}

// BulkResults shows what a bulk action did to every item: a count per outcome and one row per
// item, only the problems when there are any.
export function BulkResults({ rows, columns, summary }: { rows: BulkRow[]; columns: string[]; summary: BulkSummary }) {
  const t = useT(all)
  const bad = problems(summary)
  const [only, setOnly] = useState(bad > 0)
  const errorOf = (code: string) => {
    const msg = t(`err.${code}`)
    return msg === `err.${code}` ? code : msg
  }
  const shown = only ? rows.filter((r) => PROBLEM.has(r.result)) : rows
  return (
    <div className="stack bulk-results">
      {bad > 0 ? <Banner kind="warn" title={t('bulk.result.failed', { n: bad })} /> : <Banner kind="ok" title={t('bulk.result.ok')} />}
      <div className="bulk-counts">
        {Object.entries(summary)
          .filter(([, n]) => n > 0)
          .map(([k, n]) => (
            <span key={k} className={`pill ${tone(k)}`}>
              {t(`bulk.r.${k}`)}: {n}
            </span>
          ))}
      </div>
      {bad > 0 && <Switch checked={only} onChange={setOnly} label={t('bulk.onlyFailed')} />}
      <div className="cn-table-wrap bulk-table-wrap">
        <table className="cn-table bulk-table">
          <thead>
            <tr>
              {columns.map((c) => (
                <th key={c}>{c}</th>
              ))}
              <th>{t('bulk.col.result')}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <tr key={r.key}>
                {r.cells.map((c, i) => (
                  <td key={i}>{i === 0 ? <span className="cn-name">{c}</span> : c}</td>
                ))}
                <td>
                  <span className={`pill ${tone(r.result)}`}>{t(`bulk.r.${r.result}`)}</span>
                  {r.note && <div className="muted bulk-note">{r.note}</div>}
                  {r.error && (
                    <div className="bulk-error">
                      {errorOf(r.error)}
                      {r.detail && <span className="muted"> · {r.detail}</span>}
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
