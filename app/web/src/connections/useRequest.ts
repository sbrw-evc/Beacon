import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api'
import { useSession } from '../app/session'

function useExpiry() {
  const { expire } = useSession()
  return useCallback(
    (e: unknown) => {
      if (e instanceof ApiError && e.status === 401) expire()
    },
    [expire],
  )
}

export function useResource<T>(path: string, epoch: number) {
  const onError = useExpiry()
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const seq = useRef(0)
  // An empty path loads nothing, for resources only needed sometimes.
  const reload = useCallback(async () => {
    const mine = ++seq.current
    if (!path) return
    setBusy(true)
    try {
      const next = await api<T>('GET', path)
      if (mine !== seq.current) return
      setData(next)
      setError(null)
    } catch (e) {
      if (mine !== seq.current) return
      onError(e)
      setError(e)
    } finally {
      if (mine === seq.current) setBusy(false)
    }
  }, [path, onError])
  useEffect(() => {
    void reload()
  }, [reload, epoch])
  return { data, error, busy, reload }
}

export type ActionOptions = {
  // quietSession: a 401 or an expired password is left to the session (which signs out or asks
  // for a new password) instead of becoming the error of the action.
  quietSession?: boolean
}

// useAction runs requests and keeps the busy state and the error of the last one. run returns
// the result, or undefined when the request failed.
export function useAction({ quietSession = false }: ActionOptions = {}) {
  const onError = useExpiry()
  const { refresh } = useSession()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const run = useCallback(
    async <T>(fn: () => Promise<T>): Promise<T | undefined> => {
      setBusy(true)
      setError(null)
      try {
        return await fn()
      } catch (e) {
        onError(e)
        if (!quietSession) setError(e)
        else if (e instanceof ApiError && e.code === 'password_expired') await refresh()
        else if (!(e instanceof ApiError && e.status === 401)) setError(e)
        return undefined
      } finally {
        setBusy(false)
      }
    },
    [onError, quietSession, refresh],
  )
  const clear = useCallback(() => setError(null), [])
  return { busy, error, run, clear }
}
