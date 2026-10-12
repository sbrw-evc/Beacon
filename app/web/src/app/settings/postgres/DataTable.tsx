import type { ReactNode } from 'react'

export type Column = { label: string; numeric?: boolean; wide?: boolean }

export function DataTable({ columns, rows, caption }: { columns: Column[]; rows: { key: string | number; cells: ReactNode[] }[]; caption: string }) {
  return (
    <div className="pg-table-wrap">
      <table className="pg-table">
        <caption className="pg-sr">{caption}</caption>
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.label} scope="col" className={c.numeric ? 'num' : c.wide ? 'wide' : undefined}>
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key}>
              {r.cells.map((cell, i) => (
                <td key={i} className={columns[i].numeric ? 'num' : columns[i].wide ? 'wide' : undefined}>
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
