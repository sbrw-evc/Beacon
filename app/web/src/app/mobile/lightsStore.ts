import { useSyncExternalStore } from 'react'
import type { Severity } from '../incidents/types'

// The counts of active incidents by severity the top bar keeps an eye on, shared with the tab
// bar of the phone layout so that it does not poll the same list again. null before the first load.
export type LightCounts = Partial<Record<Severity, number>> | null

let counts: LightCounts = null
const listeners = new Set<() => void>()

export function publishCounts(next: LightCounts) {
  counts = next
  listeners.forEach((l) => l())
}

export function useLightCounts() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => counts,
  )
}
