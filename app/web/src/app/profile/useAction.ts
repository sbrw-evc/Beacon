import { useCallback, useMemo, useState } from 'react'
import { useAction as useRequest } from '../../connections/useRequest'
import { errorText, useT, type Dict } from '../../i18n'
import { strings } from '../strings'

export type Action = {
  busy: boolean
  error: { message: string; detail?: string } | null
  notice: string
  run: (fn: () => Promise<string | void>) => Promise<void>
  reset: () => void
}

// useAction is the request action of forms: the error comes translated with dict, and the
// message a successful action returns is kept as its notice.
export function useAction(dict: Dict = strings): Action {
  const t = useT(dict)
  const { busy, error, run: request, clear } = useRequest({ quietSession: true })
  const [notice, setNotice] = useState('')

  const reset = useCallback(() => {
    clear()
    setNotice('')
  }, [clear])

  const run = useCallback(
    async (fn: () => Promise<string | void>) => {
      setNotice('')
      const message = await request(fn)
      if (message) setNotice(message)
    },
    [request],
  )

  const shown = useMemo(() => (error ? errorText(t, error) : null), [t, error])
  return { busy, error: shown, notice, run, reset }
}
