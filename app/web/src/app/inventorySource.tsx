import { ExternalLink } from 'lucide-react'
import { useResource } from '../connections/useRequest'
import { useLocale, useT } from '../i18n'
import { Link } from '../router'
import { Banner, formatDate } from '../ui'
import { sourceStrings } from './inventorySourceStrings'
import { useSession } from './session'

export { sourceStrings }

// Beacon reads devices from one inventory source: Ledger, or a real NetBox at sites that run one
// instead of Ledger. The two are never on together (backend app/inventory_source.go).

export type InventorySource = {
  source: 'ledger' | 'netbox' | ''
  ledger_url?: string
  ledger_new_device_url?: string
  netbox_url?: string
  netbox_paused_at?: string
}

// useInventorySource is the inventory source for the pages that create items; null while loading.
export function useInventorySource(): InventorySource | null {
  const { can } = useSession()
  return useResource<InventorySource>(can('cis:view') ? '/api/inventory-source' : '', 0).data
}

// ledgerDeviceURL opens the form for a new device in Ledger, with its list searched for the name
// (Ledger does not fill the form from the address).
export function ledgerDeviceURL(src: InventorySource, name: string) {
  const base = src.ledger_new_device_url ?? ''
  const q = name.trim()
  return q ? `${base}&q=${encodeURIComponent(q)}` : base
}

// SourceBanner tells the NetBox and Ledger pages which inventory source is on and leads to it.
export function SourceBanner({ page, source }: { page: 'netbox' | 'ledger'; source?: InventorySource }) {
  const t = useT(sourceStrings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  if (!source) return null
  if (page === 'netbox' && source.source === 'ledger') {
    return (
      <Banner kind="warn" title={t('src.ledger.title')}>
        <p>{t('src.ledger.netbox')}</p>
        {source.netbox_paused_at && <p>{t('src.ledger.paused', { at: formatDate(source.netbox_paused_at, locale, timezone) })}</p>}
        <Link to="/ledger">{t('src.open.ledger')}</Link>
      </Banner>
    )
  }
  if (page === 'ledger' && source.source === 'netbox') {
    return (
      <Banner kind="warn" title={t('src.netbox.title')}>
        <p>{t('src.netbox.ledger')}</p>
        <Link to="/netbox">{t('src.open.netbox')}</Link>
      </Banner>
    )
  }
  return null
}

// PausedNotice is the warning of Settings → Status while NetBox is paused for Ledger.
export function PausedNotice({ source }: { source?: InventorySource }) {
  const t = useT(sourceStrings)
  const { locale } = useLocale()
  const { timezone } = useSession()
  if (!source?.netbox_paused_at) return null
  return (
    <Banner kind="warn" title={t('src.status.title')}>
      <p>{t('src.ledger.paused', { at: formatDate(source.netbox_paused_at, locale, timezone) })}</p>
      <Link to="/netbox">{t('src.open.netbox')}</Link>
    </Banner>
  )
}

// LedgerCreateLink leads to the form for a new device in Ledger.
export function LedgerCreateLink({ source, name }: { source: InventorySource; name: string }) {
  const t = useT(sourceStrings)
  return (
    <a className="btn btn-secondary" href={ledgerDeviceURL(source, name)} target="_blank" rel="noreferrer">
      <ExternalLink size={16} aria-hidden />
      {t('src.create.ledger')}
    </a>
  )
}
