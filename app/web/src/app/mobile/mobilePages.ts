import type { Can, PageDef } from '../pages'

// The overview is the home page of the phone layout; on a computer the address leads to the
// first page of the menu.
export const OVERVIEW_PATH = '/overview'

// The pages an engineer on duty needs away from the desk: what is on fire, what it hits, who owns
// it, and a maintenance window for the work. Everything else (flow editors, rules, imports,
// access and settings) is made for a computer: the phone shows a note and opens it only on request.
export const MOBILE_PAGES = new Set(['incidents', 'cmdb', 'services', 'cis', 'maintenance', 'teams', 'status'])

// The tab bar: the overview, then the first of each slot the user may open, then "More".
export const TAB_SLOTS: string[][] = [['incidents'], ['cmdb', 'services'], ['maintenance']]

export function mobileReady(p: PageDef) {
  return MOBILE_PAGES.has(p.id)
}

// The overview has something to show only with access to incidents, the map or maintenance.
export function hasOverview(can: Can) {
  return can('incidents:view') || can('cmdb:view') || can('maintenance:view')
}
