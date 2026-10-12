import { useT } from '../../i18n'
import { Select, Stepper, Switch } from '../../ui'
import { severityText, type Severity } from '../incidents/types'
import { responseStrings } from './strings'
import { CRITICALITIES, IMPACTS, SEVERITIES, type Impact, type ImpactPolicy } from './types'

export function ImpactTab({ impact, onChange, disabled }: { impact: ImpactPolicy; onChange: (v: ImpactPolicy) => void; disabled: boolean }) {
  const t = useT(responseStrings)
  const impactSelect = (value: Impact, set: (v: Impact) => void, label: string) => (
    <Select aria-label={label} value={value} disabled={disabled} onChange={(e) => set(e.target.value as Impact)}>
      {IMPACTS.map((i) => (
        <option key={i} value={i}>
          {t(`impact.${i}`)}
        </option>
      ))}
    </Select>
  )
  return (
    <fieldset className="plain-fieldset rs-impact-tab" disabled={disabled}>
      <p className="muted">{t('rs.impact.text')}</p>
      <h3 className="rs-h">{t('rs.impact.crit')}</h3>
      <div className="rs-crit-grid">
        {CRITICALITIES.map((c) => (
          <label key={c} className="rs-crit-row">
            <span className={`pill rs-crit rs-crit-${c}`}>{t(`crit.${c}`)}</span>
            {impactSelect(impact.criticality[c], (v) => onChange({ ...impact, criticality: { ...impact.criticality, [c]: v } }), t(`crit.${c}`))}
          </label>
        ))}
        <label className="rs-crit-row">
          <span className="muted">{t('rs.impact.no_service')}</span>
          {impactSelect(impact.no_service, (v) => onChange({ ...impact, no_service: v }), t('rs.impact.no_service'))}
        </label>
      </div>

      <h3 className="rs-h">{t('rs.impact.raise')}</h3>
      <Switch checked={impact.raise_red} onChange={(raise_red) => onChange({ ...impact, raise_red })} label={t('rs.impact.red')} />
      <div className="rs-raise">
        <span>{t('rs.impact.dependents')}</span>
        <Stepper value={impact.raise_dependents} min={0} max={1000} label={t('rs.impact.dependents')} onChange={(raise_dependents) => onChange({ ...impact, raise_dependents })} />
      </div>
      <div className="rs-raise">
        <span>{t('rs.impact.incidents')}</span>
        <Stepper value={impact.raise_incidents} min={0} max={1000} label={t('rs.impact.incidents')} onChange={(raise_incidents) => onChange({ ...impact, raise_incidents })} />
      </div>

      <h3 className="rs-h">{t('rs.impact.matrix')}</h3>
      <p className="hint">{t('rs.impact.matrix.hint')}</p>
      <div className="rs-matrix-wrap">
        <table className="cn-table compact rs-matrix">
          <thead>
            <tr>
              <th />
              {SEVERITIES.map((s) => (
                <th key={s}>{severityText(t, s)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {IMPACTS.map((i) => (
              <tr key={i}>
                <th scope="row">{t(`impact.${i}`)}</th>
                {SEVERITIES.map((s) => (
                  <td key={s}>
                    <Select
                      aria-label={`${t(`impact.${i}`)} × ${severityText(t, s)}`}
                      className={`rs-cell rs-cell-${impact.matrix[i][s]}`}
                      value={impact.matrix[i][s]}
                      disabled={disabled}
                      onChange={(e) => onChange({ ...impact, matrix: { ...impact.matrix, [i]: { ...impact.matrix[i], [s]: e.target.value as Severity } } })}
                    >
                      {SEVERITIES.map((p) => (
                        <option key={p} value={p}>
                          {severityText(t, p)}
                        </option>
                      ))}
                    </Select>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Switch checked={impact.never_lower} onChange={(never_lower) => onChange({ ...impact, never_lower })} label={t('rs.impact.never_lower')} />
    </fieldset>
  )
}
