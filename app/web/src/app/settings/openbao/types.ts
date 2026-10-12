import type { OpenBaoReport, OpenBaoStatus } from '../../../connections/openbao'

export type OpenBaoOverview = {
  connection: { addr: string; mount: string; namespace?: string; auth: string; approle_path?: string; custom_ca: boolean; skip_verify: boolean }
  status: OpenBaoStatus
  secrets: number
  list_error?: string
}

export type OpenBaoTargetReport = OpenBaoReport & { kv2_error?: string; secrets: number }

export type OpenBaoMigrated = { addr: string; mount: string; copied: number; refs: number }
