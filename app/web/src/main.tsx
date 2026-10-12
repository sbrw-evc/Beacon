import { StrictMode, Suspense, lazy, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { api, type Meta, type Theme } from './api'
import { browserLocale, LocaleProvider, savedLocale, useT } from './i18n'
import { installScrollbars } from './scrollbars'
import { applyTheme, savedTheme, ThemeProvider } from './theme'
import { Button } from './ui'
import './styles.css'
import './controls.css'

const SetupApp = lazy(() => import('./setup/SetupApp'))
const MainApp = lazy(() => import('./app/MainApp'))

function systemTheme(): Theme {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function Boot() {
  const [meta, setMeta] = useState<Meta | null>(null)
  const [failed, setFailed] = useState(false)

  const load = () => {
    setFailed(false)
    api<Meta>('GET', '/api/meta')
      .then(setMeta)
      .catch(() => setFailed(true))
  }
  useEffect(load, [])

  if (!meta) {
    const locale = savedLocale() ?? browserLocale()
    return (
      <LocaleProvider key="boot" initial={locale}>
        <BootScreen failed={failed} retry={load} />
      </LocaleProvider>
    )
  }

  const setup = meta.mode === 'setup'
  const locale = savedLocale() ?? (setup ? browserLocale() : meta.default_locale || 'en')
  const theme = savedTheme() ?? (setup ? systemTheme() : meta.default_theme || 'light')
  return (
    <LocaleProvider key={meta.mode} initial={locale}>
      <ThemeProvider initial={theme}>
        <Suspense fallback={<BootScreen failed={false} retry={load} />}>{setup ? <SetupApp meta={meta} onReady={load} /> : <MainApp meta={meta} />}</Suspense>
      </ThemeProvider>
    </LocaleProvider>
  )
}

function BootScreen({ failed, retry }: { failed: boolean; retry: () => void }) {
  const t = useT()
  return (
    <div className="center-page">
      <img src="/logo.svg" alt="" width={48} height={48} />
      {failed ? (
        <>
          <p className="muted">{t('err.network')}</p>
          <Button onClick={retry}>{t('retry')}</Button>
        </>
      ) : (
        <p className="boot">{t('loading')}</p>
      )}
    </div>
  )
}

applyTheme(savedTheme() ?? systemTheme())
installScrollbars()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boot />
  </StrictMode>,
)
