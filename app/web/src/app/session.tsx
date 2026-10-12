import { createContext, useContext, type ReactNode } from 'react'
import type { PasswordPolicy } from '../policy'
import type { User } from './types'

export type Session = {
  user: User
  timezone: string
  defaultTz: string
  policy: PasswordPolicy
  update: (u: User) => void
  refresh: () => Promise<void>
  setPolicy: (p: PasswordPolicy) => void
  expire: () => void
  can: (perm: string) => boolean
  setDefaultTz: (tz: string) => void
}

const SessionContext = createContext<Session | null>(null)

export function SessionProvider({ value, children }: { value: Session; children: ReactNode }) {
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession() {
  const s = useContext(SessionContext)
  if (!s) throw new Error('useSession outside SessionProvider')
  return s
}
