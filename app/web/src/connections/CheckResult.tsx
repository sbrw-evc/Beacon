import type { ReactNode } from 'react'
import { useT, type Dict } from '../i18n'
import { Banner } from '../ui'
import { Flash } from '../notify'

export type Check<T> = { key: string; ok: boolean; result?: T; error?: string } | null

const checkStrings: Dict = {
  en: { 'check.changed': 'Settings changed after the check. Check the connection again.' },
  ru: { 'check.changed': 'Настройки изменились после проверки. Проверьте подключение ещё раз.' },
}

export function CheckResult<T>({
  check,
  currentKey,
  ok,
  failTitle,
  failExtra,
}: {
  check: Check<T>
  currentKey: string
  ok: (r: T) => ReactNode
  failTitle: string
  failExtra?: (error: string) => string | undefined
}) {
  const t = useT(checkStrings)
  if (!check) return null
  if (check.key !== currentKey) return <Banner kind="info" title={t('check.changed')} />
  if (check.ok && check.result) return <>{ok(check.result)}</>
  const extra = failExtra?.(check.error ?? '')
  return (
    <Flash kind="error" title={failTitle} trigger={check}>
      {check.error && <div>{check.error}</div>}
      {extra && <div>{extra}</div>}
    </Flash>
  )
}
