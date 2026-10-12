import type { CSSProperties, ReactNode } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Choices } from './Choice'
import { useT } from './i18n'
import { MAX_AGE_DAYS, MAX_WARN_DAYS, expiryText, policyError, ruleText, rules, type Letters, type PasswordPolicy } from './policy'
import { policyEditorStrings } from './policyEditorStrings'
import { Banner, Field, SettingRow, Stepper, Switch } from './ui'

const LETTERS: Letters[] = ['latin', 'cyrillic', 'latin_cyrillic', 'any']
const DEFAULT_MAX_AGE = 90
const DEFAULT_WARN = 14

function Reveal({ show, children }: { show: boolean; children: ReactNode }) {
  return (
    <AnimatePresence initial={false}>
      {show && (
        <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} className="reveal-box">
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  )
}

function AtLeast({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
  return (
    <div className="at-least">
      <span className="hint">{label}</span>
      <Stepper label={label} min={1} max={16} value={value} onChange={onChange} />
    </div>
  )
}

export function PolicyEditor({ value: policy, onChange }: { value: PasswordPolicy; onChange: (p: PasswordPolicy) => void }) {
  const t = useT(policyEditorStrings)
  const set = (patch: Partial<PasswordPolicy>) => onChange({ ...policy, ...patch })
  const problem = policyError(policy)
  const expires = policy.max_age_days > 0

  return (
    <div className="stack">
      <Field label={t('pol.length')}>
        {(id) => (
          <div className="range-row">
            <input
              type="range"
              min={8}
              max={64}
              value={Math.min(policy.min_length, 64)}
              onChange={(e) => set({ min_length: Number(e.target.value) })}
              style={{ '--fill': `${((Math.min(policy.min_length, 64) - 8) / (64 - 8)) * 100}%` } as CSSProperties}
              aria-label={t('pol.length')}
            />
            <Stepper id={id} label={t('pol.length')} min={8} max={128} value={policy.min_length} onChange={(v) => set({ min_length: v })} />
          </div>
        )}
      </Field>
      <div className="section">
        <Switch
          checked={policy.require_digits}
          onChange={(v) => set({ require_digits: v, min_digits: v ? Math.max(policy.min_digits, 1) : policy.min_digits })}
          label={t('pol.digits')}
          hint={t('pol.digits.hint')}
          aside={policy.require_digits && <AtLeast label={t('pol.count')} value={policy.min_digits} onChange={(v) => set({ min_digits: v })} />}
        />
      </div>
      <div className="section">
        <Switch
          checked={policy.require_special}
          onChange={(v) => set({ require_special: v, min_special: v ? Math.max(policy.min_special, 1) : policy.min_special })}
          label={t('pol.special')}
          hint={t('pol.special.hint')}
          aside={policy.require_special && <AtLeast label={t('pol.count')} value={policy.min_special} onChange={(v) => set({ min_special: v })} />}
        />
      </div>
      <div className="section">
        <Switch checked={policy.require_mixed_case} onChange={(v) => set({ require_mixed_case: v })} label={t('pol.case')} hint={t('pol.case.hint')} />
      </div>
      <Field label={t('pol.letters')} hint={t('pol.letters.hint')}>
        {() => (
          <Choices
            label={t('pol.letters')}
            value={policy.letters}
            onChange={(l) => set({ letters: l })}
            options={LETTERS.map((l) => ({ value: l, title: t(`pol.letters.${l}`), hint: t(`pol.letters.${l}.ex`) }))}
          />
        )}
      </Field>
      <div className="section">
        <Switch
          checked={expires}
          onChange={(v) => set(v ? { max_age_days: DEFAULT_MAX_AGE, warn_days: DEFAULT_WARN } : { max_age_days: 0, warn_days: 0 })}
          label={t('pol.expire')}
          hint={t('pol.expire.hint')}
        />
        <Reveal show={expires}>
          <div className="setting-rows">
            <SettingRow label={t('pol.maxAge')} hint={t('pol.maxAge.hint')}>
              <Stepper label={t('pol.maxAge')} min={1} max={MAX_AGE_DAYS} value={policy.max_age_days} onChange={(v) => set({ max_age_days: v })} />
            </SettingRow>
            <SettingRow label={t('pol.warn')} hint={t('pol.warn.hint')}>
              <Stepper label={t('pol.warn')} min={0} max={MAX_WARN_DAYS} value={policy.warn_days} onChange={(v) => set({ warn_days: v })} />
            </SettingRow>
          </div>
        </Reveal>
      </div>
      {problem ? (
        <Banner kind="error" title={t(`pol.err.${problem}`)} />
      ) : (
        <Banner kind="info" title={t('pol.summary')}>
          <ul className="checklist" style={{ marginTop: 6, color: 'var(--text)' }}>
            {rules(policy).map((r) => (
              <li key={r}>• {ruleText(t, r, policy)}</li>
            ))}
            <li>• {expiryText(t, policy)}</li>
          </ul>
        </Banner>
      )}
    </div>
  )
}
