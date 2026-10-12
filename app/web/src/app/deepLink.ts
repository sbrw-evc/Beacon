import { useEffect } from 'react'
import { api } from '../api'

// useDeepLink opens the card named by ?id= in the address (a link from an incident card): the
// item is loaded from `${base}/${id}` and handed to open once; the parameter is then dropped, so
// a reload or an edit does not open the card again.
export function useDeepLink<T>(base: string, open: (item: T) => void) {
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get('id')
    if (!id) return
    let live = true
    api<T>('GET', `${base}/${encodeURIComponent(id)}`)
      .then((item) => {
        if (!live) return
        open(item)
        clearDeepLink()
      })
      .catch(() => live && clearDeepLink())
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
}

export function clearDeepLink() {
  const p = new URLSearchParams(window.location.search)
  if (!p.has('id')) return
  p.delete('id')
  const s = p.toString()
  window.history.replaceState(null, '', `${window.location.pathname}${s ? `?${s}` : ''}`)
}
