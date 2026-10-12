import { useEffect, useState } from 'react'
import { api, type Locale, type Theme } from '../api'
import { errorText, useT } from '../i18n'
import { Button, Field, Modal, Segmented, TimezoneSelect } from '../ui'
import { strings } from './strings'
import { Flash } from '../notify'

export type Defaults = { default_theme: Theme; default_locale: Locale; default_timezone: string }

export function DefaultsEditor({ open, onClose, initial, onSaved }: { open: boolean; onClose: () => void; initial: Defaults; onSaved: (d: Defaults) => void }) {
  const t = useT(strings)
  const [d, setD] = useState<Defaults>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null)

  useEffect(() => {
    if (open) {
      setD({ default_theme: initial.default_theme, default_locale: initial.default_locale, default_timezone: initial.default_timezone })
      setError(null)
    }
  }, [open, initial.default_theme, initial.default_locale, initial.default_timezone])

  const changed = d.default_theme !== initial.default_theme || d.default_locale !== initial.default_locale || d.default_timezone !== initial.default_timezone

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      onSaved(await api<Defaults>('PUT', '/api/settings', d))
      onClose()
    } catch (e) {
      setError(errorText(t, e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t('settings.title')}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t('cancel')}
          </Button>
          <Button variant="primary" onClick={save} busy={busy} disabled={!changed}>
            {t('save')}
          </Button>
        </>
      }
    >
      <p className="muted">{t('defaults.text')}</p>
      <Field label={t('field.theme')}>
        {() => (
          <Segmented
            label={t('field.theme')}
            value={d.default_theme}
            onChange={(v) => setD({ ...d, default_theme: v })}
            options={[
              { value: 'light', label: t('theme.light') },
              { value: 'dark', label: t('theme.dark') },
            ]}
          />
        )}
      </Field>
      <Field label={t('field.locale')}>
        {() => (
          <Segmented
            label={t('field.locale')}
            value={d.default_locale}
            onChange={(v) => setD({ ...d, default_locale: v })}
            options={[
              { value: 'en', label: t('lang.en') },
              { value: 'ru', label: t('lang.ru') },
            ]}
          />
        )}
      </Field>
      <Field label={t('field.timezone')}>
        {(id) => <TimezoneSelect id={id} value={d.default_timezone} onChange={(v) => setD({ ...d, default_timezone: v })} />}
      </Field>
      {error && <Flash kind="error" title={error.message}>{error.detail}</Flash>}
    </Modal>
  )
}
