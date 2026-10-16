import { useState } from 'react'
import { SetupGuide } from '../../guide/SetupGuide'
import { FlowCard } from './FlowCard'
import { GrafanaCard } from './GrafanaCard'
import { NotifyCard } from './NotifyCard'
import { PagerDutyCard } from './PagerDutyCard'
import { PolicyCard } from './PolicyCard'
import { PublicURLCard } from './PublicURLCard'
import './alerting.css'

export function AlertingSettings() {
  // Whether links work in backup notification and the PagerDuty webhook address depend on the
  // Beacon address; the cards read it again when it is saved.
  const [epoch, setEpoch] = useState(0)
  return (
    <>
      <SetupGuide page="settings.alerting" />
      <PublicURLCard onSaved={() => setEpoch((e) => e + 1)} />
      <PagerDutyCard reloadKey={epoch} />
      <NotifyCard key={epoch} />
      <GrafanaCard />
      <FlowCard />
      <PolicyCard />
    </>
  )
}
