import { useEffect, useRef, useSyncExternalStore } from 'react'

// One EventSource on /api/incidents/stream serves every component of the page: it opens with
// the first listener and closes with the last. The server sends "change" whenever an incident or
// its timeline changes, and "ready" on each (re)connect, after which the page catches up too.

type Listener = () => void

const listeners = new Set<Listener>()
const watchers = new Set<() => void>()
let source: EventSource | null = null
let live = false
let retry = 0

function setLive(v: boolean) {
  if (live === v) return
  live = v
  for (const w of watchers) w()
}

function notify() {
  for (const l of listeners) l()
}

function open() {
  window.clearTimeout(retry)
  const es = new EventSource('/api/incidents/stream')
  source = es
  es.addEventListener('ready', () => {
    setLive(true)
    notify()
  })
  es.addEventListener('change', notify)
  es.onerror = () => {
    setLive(false)
    // The browser reconnects by itself unless the answer was not a stream (401, 503): then the
    // stream is opened again a little later.
    if (es.readyState === EventSource.CLOSED && source === es) {
      source = null
      retry = window.setTimeout(() => listeners.size > 0 && !source && open(), 10_000)
    }
  }
}

function close() {
  window.clearTimeout(retry)
  source?.close()
  source = null
  setLive(false)
}

function subscribe(l: Listener) {
  listeners.add(l)
  if (!source && typeof EventSource !== 'undefined') open()
  return () => {
    listeners.delete(l)
    if (listeners.size === 0) close()
  }
}

// useIncidentFeed calls onChange whenever incidents change and reports whether the stream is up.
export function useIncidentFeed(onChange: () => void): boolean {
  const cb = useRef(onChange)
  cb.current = onChange
  useEffect(() => subscribe(() => cb.current()), [])
  return useSyncExternalStore(
    (w) => {
      watchers.add(w)
      return () => watchers.delete(w)
    },
    () => live,
  )
}

// useLiveReload calls reload when incidents change while the tab is visible; changes that come
// while it is hidden are caught up when it is shown again. It reports whether the stream is up.
export function useLiveReload(reload: () => void): boolean {
  const stale = useRef(false)
  const live = useIncidentFeed(() => {
    if (document.visibilityState === 'visible') reload()
    else stale.current = true
  })
  useEffect(() => {
    const onShow = () => {
      if (document.visibilityState === 'visible' && stale.current) {
        stale.current = false
        reload()
      }
    }
    document.addEventListener('visibilitychange', onShow)
    return () => document.removeEventListener('visibilitychange', onShow)
  }, [reload])
  return live
}
