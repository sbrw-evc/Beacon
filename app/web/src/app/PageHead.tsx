import { useT } from '../i18n'
import { navStrings } from './navStrings'
import type { PageDef } from './pages'

export function PageHead({ page }: { page: PageDef }) {
  const t = useT(navStrings)
  return (
    <div className="page-head">
      <div>
        <h1>{t(`page.${page.id}`)}</h1>
        {page.subtitle && <p className="muted">{t(page.subtitle)}</p>}
      </div>
    </div>
  )
}
