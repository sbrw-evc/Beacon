import { Check } from 'lucide-react'
import { motion } from 'motion/react'
import { useLocale, useT } from '../../i18n'
import { Switch } from '../../ui'
import { Permissions, VIEW, type Catalog, type CatalogPage } from './permissions'
import { strings } from './strings'

export function PermissionMatrix({
  catalog,
  value,
  onChange,
  readOnly,
}: {
  catalog: Catalog
  value: Permissions
  onChange: (v: Permissions) => void
  readOnly: boolean
}) {
  const { locale } = useLocale()
  return (
    <div className="perm-matrix">
      {catalog.groups.map((g) => {
        const pages = catalog.pages.filter((p) => p.group === g.id)
        if (pages.length === 0) return null
        return (
          <section key={g.id} className="perm-group" aria-label={g.title[locale]}>
            <div className="section-title">{g.title[locale]}</div>
            {pages.map((p) => (
              <PageRow key={p.id} page={p} value={value} onChange={onChange} readOnly={readOnly} />
            ))}
          </section>
        )
      })}
    </div>
  )
}

function PageRow({ page, value, onChange, readOnly }: { page: CatalogPage; value: Permissions; onChange: (v: Permissions) => void; readOnly: boolean }) {
  const t = useT(strings)
  const { locale } = useLocale()
  const count = value.count(page)
  return (
    <div className={`perm-page${count > 0 ? ' granted' : ''}`}>
      <div className="perm-page-head">
        <span className="perm-page-title">
          {page.title[locale]}
          <span className="hint">{t('roles.matrix.count', { n: count, total: page.features.length })}</span>
        </span>
        {!readOnly && (
          <span className="perm-all">
            <Switch checked={value.all(page)} onChange={(on) => onChange(value.setAll(page, on))} label={t('roles.matrix.all')} />
          </span>
        )}
      </div>
      <div className="perm-features" role="group" aria-label={page.title[locale]}>
        {page.features.map((f) => {
          const on = value.has(page.id, f.id)
          return (
            <motion.button
              key={f.id}
              type="button"
              className={`chip perm-chip${on ? ' on' : ''}${f.id === VIEW ? ' view' : ''}`}
              aria-pressed={on}
              disabled={readOnly}
              onClick={() => onChange(value.toggle(page, f.id, !on))}
              whileTap={readOnly ? undefined : { scale: 0.96 }}
            >
              <span className="perm-tick" aria-hidden>
                {on && <Check size={12} />}
              </span>
              {f.title[locale]}
            </motion.button>
          )
        })}
      </div>
    </div>
  )
}
