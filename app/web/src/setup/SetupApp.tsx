import { useEffect, useRef, useState, type FormEvent, type ReactNode, type RefObject } from 'react'
import { Check } from 'lucide-react'
import { AnimatePresence, motion, MotionConfig, type Variants } from 'motion/react'
import { api, ApiError, type Locale, type Meta, type Theme } from '../api'
import { errorText, useLocale, useT } from '../i18n'
import { useTheme } from '../theme'
import { Banner, Brand, browserTimezone, Button, Field, Input, Password, Preferences, Rows, spring, Switch, TimezoneSelect, timezones, zoneLabel } from '../ui'
import { strings } from './strings'
import { Choice, ChoiceMark, Pop } from '../Choice'
import type { Check as Probe } from '../connections/CheckResult'
import { openBaoBody, openBaoCheckKey, openBaoDraft, type OpenBaoDraft, type OpenBaoReport } from '../connections/openbao'
import { OpenBaoCheckResult, OpenBaoForm } from '../connections/OpenBaoForm'
import { openBaoStrings } from '../connections/openbaoStrings'
import {
  postgresBody,
  postgresCheckKey,
  postgresComplete,
  postgresDraft,
  postgresUsable,
  type PostgresDraft,
  type PostgresReport,
} from '../connections/postgres'
import { PostgresCheckResult, PostgresForm } from '../connections/PostgresForm'
import { postgresStrings } from '../connections/postgresStrings'
import { originOf } from '../fx'
import { Toaster } from '../notify'

const TOKEN_HEADER = 'X-Setup-Token'

// Nobody signs in to Umbrella with a password: people sign in at SPA, and the first administrator
// is whoever SPA gives the role «Administrator». The SPA step takes the connection key (optional:
// it can also come from SPA_CONNECTION_KEY).
type StepId = 'code' | 'prefs' | 'openbao' | 'postgres' | 'spa' | 'review'
const STEPS: StepId[] = ['code', 'prefs', 'openbao', 'postgres', 'spa', 'review']

type SpaReport = { ok: boolean; issuer?: string; application?: string; error?: string }

type Postgres = PostgresDraft & { reuse_existing: boolean }

const dict = {
  en: { ...strings.en, ...openBaoStrings.en, ...postgresStrings.en },
  ru: { ...strings.ru, ...openBaoStrings.ru, ...postgresStrings.ru },
}

const stepVariants: Variants = {
  enter: (dir: 'fwd' | 'back') => ({ opacity: 0, x: dir === 'fwd' ? 36 : -36 }),
  center: { opacity: 1, x: 0 },
  exit: (dir: 'fwd' | 'back') => ({ opacity: 0, x: dir === 'fwd' ? -36 : 36 }),
}

// publicURLOk mirrors the server check: http or https, a host, no query, fragment or user.
export function publicURLOk(v: string) {
  const s = v.trim()
  if (s === '') return true
  try {
    const u = new URL(s)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== '' && !u.username && !u.password && !u.search && !u.hash && !/[?#]/.test(s)
  } catch {
    return false
  }
}

function stepOf(code: string): StepId | null {
  if (code === 'invalid_setup_token') return 'code'
  if (code.startsWith('openbao_')) return 'openbao'
  if (code.startsWith('postgres_')) return 'postgres'
  if (code.startsWith('spa_')) return 'spa'
  if (code === 'public_url_invalid') return 'prefs'
  if (code === 'invalid_locale' || code === 'invalid_theme' || code === 'invalid_timezone') return 'prefs'
  return null
}

function pgBody(p: Postgres) {
  return { ...postgresBody(p), reuse_existing: p.reuse_existing }
}

export default function SetupApp({ meta, onReady }: { meta: Meta; onReady: () => void }) {
  const t = useT(dict)
  const { locale, setLocale } = useLocale()
  const { theme, setTheme } = useTheme()

  const [step, setStepRaw] = useState<StepId>('code')
  const [dir, setDir] = useState<'fwd' | 'back'>('fwd')
  const setStep = (s: StepId) => {
    setDir(STEPS.indexOf(s) >= STEPS.indexOf(step) ? 'fwd' : 'back')
    setStepRaw(s)
  }
  const [reached, setReached] = useState(0)
  const [code, setCode] = useState('')
  const [codeOk, setCodeOk] = useState(false)
  const [defaults, setDefaults] = useState<{ locale: Locale; theme: Theme; timezone: string }>(() => {
    const tz = browserTimezone()
    return { locale, theme, timezone: timezones().some((z) => z.id === tz) ? tz : 'UTC' }
  })
  const [ob, setOb] = useState<OpenBaoDraft>(openBaoDraft)
  const [pg, setPg] = useState<Postgres>(() => ({ ...postgresDraft(), reuse_existing: false }))
  const [spaKey, setSpaKey] = useState('')
  // The address people reach Umbrella at: the wizard is opened at it in most installations.
  const [publicURL, setPublicURL] = useState(() => window.location.origin)
  const [obCheck, setObCheck] = useState<Probe<OpenBaoReport>>(null)
  const [pgCheck, setPgCheck] = useState<Probe<PostgresReport>>(null)
  const [spaCheck, setSpaCheck] = useState<Probe<SpaReport>>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ message: string; detail?: string; step?: StepId | null } | null>(null)
  const [done, setDone] = useState(false)
  const heading = useRef<HTMLHeadingElement>(null)

  const headers = { [TOKEN_HEADER]: code.trim() }
  const obKey = openBaoCheckKey(ob)
  const pgKey = postgresCheckKey(pg)
  const spaCheckKey = `${spaKey.trim()}\n${publicURL.trim()}`

  const obValid = obCheck?.ok === true && obCheck.key === obKey
  const pgValid = pgCheck?.ok === true && pgCheck.key === pgKey && pgCheck.result !== undefined && postgresUsable(pgCheck.result.probe)
  const pgNeedsReuse = pgValid && pgCheck?.result?.probe.has_state === true
  const spaValid = spaKey.trim() === '' || (spaCheck?.ok === true && spaCheck.key === spaCheckKey)
  const publicValid = publicURLOk(publicURL)

  const canNext: Record<StepId, boolean> = {
    code: codeOk,
    prefs: publicValid,
    openbao: obValid,
    postgres: pgValid && (!pgNeedsReuse || pg.reuse_existing),
    spa: spaValid,
    review: false,
  }

  useEffect(() => {
    heading.current?.focus()
    setError(null)
  }, [step])

  const go = (s: StepId) => {
    const i = STEPS.indexOf(s)
    setReached((r) => Math.max(r, i))
    setStep(s)
  }

  const next = () => {
    const i = STEPS.indexOf(step)
    if (canNext[step] && i < STEPS.length - 1) go(STEPS[i + 1])
  }

  const back = () => {
    const i = STEPS.indexOf(step)
    if (i > 0) setStep(STEPS[i - 1])
  }

  const run = async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true)
    setError(null)
    try {
      return await fn()
    } catch (e) {
      const target = e instanceof ApiError ? stepOf(e.code) : null
      setError({ ...errorText(t, e), step: target })
      if (e instanceof ApiError && e.code === 'invalid_setup_token') {
        setCodeOk(false)
      }
      return undefined
    } finally {
      setBusy(false)
    }
  }

  const verifyCode = (e: FormEvent) => {
    e.preventDefault()
    void run(async () => {
      await api('POST', '/api/setup/token', undefined, headers)
      setCodeOk(true)
      go('prefs')
    })
  }

  const testOb = () =>
    run(async () => {
      const key = obKey
      const r = await api<OpenBaoReport>('POST', '/api/setup/openbao/test', openBaoBody(ob), headers)
      setObCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const testPg = () =>
    run(async () => {
      const key = pgKey
      const r = await api<PostgresReport>('POST', '/api/setup/postgres/test', pgBody(pg), headers)
      setPgCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const testSpa = () =>
    run(async () => {
      const key = spaCheckKey
      const r = await api<SpaReport>('POST', '/api/setup/spa/test', { spa_connection_key: spaKey.trim(), public_url: publicURL.trim() || window.location.origin }, headers)
      setSpaCheck({ key, ok: r.ok, result: r, error: r.error })
    })

  const finish = () =>
    run(async () => {
      await api(
        'POST',
        '/api/setup/complete',
        {
          locale: defaults.locale,
          theme: defaults.theme,
          timezone: defaults.timezone,
          public_url: publicURL.trim().replace(/\/+$/, ''),
          openbao: openBaoBody(ob),
          postgres: pgBody(pg),
          spa_connection_key: spaKey.trim(),
        },
        headers,
      )
      setDone(true)
    })

  if (done) {
    return (
      <div className="center-page">
        <motion.div
          className="card panel"
          style={{ width: 'min(520px, 100%)' }}
          initial={{ opacity: 0, scale: 0.9, y: 12 }}
          animate={{ opacity: 1, scale: 1, y: 0 }}
          transition={{ type: 'spring', stiffness: 260, damping: 22 }}
        >
          <svg className="done-mark" viewBox="0 0 52 52" width={56} height={56} aria-hidden>
            <motion.circle
              cx="26"
              cy="26"
              r="24"
              initial={{ pathLength: 0 }}
              animate={{ pathLength: 1 }}
              transition={{ duration: 0.6, delay: 0.15, ease: 'easeOut' }}
            />
            <motion.path
              d="M15 27l7 7 15-15"
              initial={{ pathLength: 0 }}
              animate={{ pathLength: 1 }}
              transition={{ duration: 0.4, delay: 0.65, ease: 'easeOut' }}
            />
          </svg>
          <h1>{t('done.title')}</h1>
          <p className="muted">{t(spaKey.trim() ? 'done.text' : 'done.text.nokey')}</p>
          <Button variant="primary" onClick={onReady}>
            {t('done.signin')}
          </Button>
        </motion.div>
      </div>
    )
  }

  const current = STEPS.indexOf(step)
  const errorBanner = error && (
    <Banner kind="error" title={error.message}>
      {error.detail && <div>{error.detail}</div>}
      {error.step && error.step !== step && (
        <Button variant="ghost" onClick={() => setStep(error.step!)} style={{ marginTop: 8, paddingLeft: 0 }}>
          {t('rv.goto')}: {t(`step.${error.step}`)}
        </Button>
      )}
    </Banner>
  )

  return (
    <MotionConfig reducedMotion="user">
      <div className="wizard">
        <header className="wizard-top">
          <Brand subtitle={`${t('title')} · ${meta.version}`} />
          <Preferences persist={false} />
        </header>
        <div className="wizard-body">
          <nav aria-label={t('title')}>
            <ol className="steps">
              {STEPS.map((s, i) => {
                const isDone = i < current || (i <= reached && canNext[s] && s !== step)
                return (
                  <li key={s}>
                    <button
                      type="button"
                      className={`step ${s === step ? 'current' : ''} ${isDone ? 'done' : ''}`}
                      disabled={i > reached || (!codeOk && s !== 'code')}
                      aria-current={s === step ? 'step' : undefined}
                      onClick={() => setStep(s)}
                    >
                      {s === step && <motion.span layoutId="step-current" className="step-highlight" transition={spring} />}
                      <span className="num">
                        {isDone ? (
                          <Pop>
                            <Check size={14} />
                          </Pop>
                        ) : (
                          i + 1
                        )}
                      </span>
                      <span className="step-label">
                        <span>{t(`step.${s}`)}</span>
                        <small>{t(`step.${s}.sub`)}</small>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ol>
          </nav>

          <main className="card panel">
            <div className="progress" role="progressbar" aria-valuemin={1} aria-valuemax={STEPS.length} aria-valuenow={current + 1}>
              <motion.span
                initial={false}
                animate={{ width: `${((current + 1) / STEPS.length) * 100}%` }}
                transition={{ type: 'spring', stiffness: 140, damping: 22 }}
              />
            </div>
            <AnimatePresence mode="wait" custom={dir} initial={false}>
              <motion.div
                key={step}
                className="step-view"
                custom={dir}
                variants={stepVariants}
                initial="enter"
                animate="center"
                exit="exit"
                transition={{ duration: 0.26, ease: [0.22, 1, 0.36, 1] }}
              >
                {step === 'code' && (
                  <form className="stack" onSubmit={verifyCode} noValidate>
                    <Head refEl={heading} title={t('code.title')} text={t('code.text')} />
                    <div className="section">
                      <div className="section-title">{t('code.where')}</div>
                      <Rows
                        rows={[
                          [t('code.logs'), <code key="l">docker compose logs umbrella | grep setup_code</code>],
                          [t('code.file'), <code key="f">/data/setup-token</code>],
                        ]}
                      />
                    </div>
                    <Field label={t('code.label')}>
                      {(id) => <Input id={id} value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" spellCheck={false} autoFocus />}
                    </Field>
                    {errorBanner}
                    <div className="panel-foot">
                      <span />
                      <Button variant="primary" type="submit" busy={busy} disabled={!code.trim()}>
                        {t('code.verify')}
                      </Button>
                    </div>
                  </form>
                )}

                {step === 'prefs' && (
                  <div className="stack">
                    <Head refEl={heading} title={t('prefs.title')} text={t('prefs.text')} />
                    <Field label={t('prefs.locale')}>
                      {() => (
                        <div className="choices" role="radiogroup" aria-label={t('prefs.locale')}>
                          {(['en', 'ru'] as Locale[]).map((l) => (
                            <Choice
                              key={l}
                              aria-checked={defaults.locale === l}
                              onClick={() => {
                                setDefaults((d) => ({ ...d, locale: l }))
                                setLocale(l, false)
                              }}
                            >
                              <ChoiceMark checked={defaults.locale === l} />
                              <span className="choice-title">{t(`lang.${l}`)}</span>
                            </Choice>
                          ))}
                        </div>
                      )}
                    </Field>
                    <Field label={t('prefs.theme')}>
                      {() => (
                        <div className="choices" role="radiogroup" aria-label={t('prefs.theme')}>
                          {(['light', 'dark'] as Theme[]).map((v) => (
                            <Choice
                              key={v}
                              aria-checked={defaults.theme === v}
                              onClick={(e) => {
                                setDefaults((d) => ({ ...d, theme: v }))
                                setTheme(v, false, originOf(e))
                              }}
                            >
                              <span className={`swatch swatch-${v}`} aria-hidden />
                              <span>
                                <span className="choice-title">{t(`theme.${v}`)}</span>
                                <br />
                                <span className="hint">{t(`prefs.${v}.desc`)}</span>
                              </span>
                            </Choice>
                          ))}
                        </div>
                      )}
                    </Field>
                    <Field label={t('prefs.timezone')} hint={t('prefs.timezone.hint')}>
                      {(id) => <TimezoneSelect id={id} value={defaults.timezone} onChange={(v) => setDefaults((d) => ({ ...d, timezone: v }))} />}
                    </Field>
                    <Field label={t('prefs.public')} hint={publicValid ? t('prefs.public.hint') : t('prefs.public.bad')}>
                      {(id) => (
                        <Input
                          id={id}
                          value={publicURL}
                          placeholder="https://umbrella.example.com"
                          spellCheck={false}
                          aria-invalid={!publicValid || undefined}
                          onChange={(e) => setPublicURL(e.target.value)}
                        />
                      )}
                    </Field>
                    {errorBanner}
                    <Foot back={back} next={next} canNext={canNext.prefs} t={t} />
                  </div>
                )}

                {step === 'openbao' && (
                  <div className="stack">
                    <Head refEl={heading} title={t('ob.title')} text={t('ob.text')} />
                    <OpenBaoForm value={ob} onChange={setOb} />
                    <OpenBaoCheckResult check={obCheck} draft={ob} container={meta.container} />
                    {errorBanner}
                    <Foot back={back} next={next} canNext={canNext.openbao} t={t} check={testOb} busy={busy} checkDisabled={!ob.addr.trim()} />
                  </div>
                )}

                {step === 'postgres' && (
                  <div className="stack">
                    <Head refEl={heading} title={t('pg.title')} text={t('pg.text')} />
                    <PostgresForm value={pg} onChange={(d) => setPg({ ...pg, ...d })} />
                    <PostgresCheckResult
                      check={pgCheck}
                      draft={pg}
                      container={meta.container}
                      state={
                        <>
                          <p>{t('pg.state.text')}</p>
                          <div style={{ marginTop: 10 }}>
                            <Switch checked={pg.reuse_existing} onChange={(v) => setPg({ ...pg, reuse_existing: v })} label={t('pg.reuse')} />
                          </div>
                        </>
                      }
                    />
                    {errorBanner}
                    <Foot back={back} next={next} canNext={canNext.postgres} t={t} check={testPg} busy={busy} checkDisabled={!postgresComplete(pg)} />
                  </div>
                )}

                {step === 'spa' && (
                  <div className="stack">
                    <Head refEl={heading} title={t('spa.title')} text={t('spa.text')} />
                    <Field label={t('spa.key')} optional={t('optional')} hint={t('spa.key.hint')}>
                      {(id) => <Password id={id} value={spaKey} onChange={(e) => setSpaKey(e.target.value)} autoComplete="off" spellCheck={false} placeholder="spa1.…" />}
                    </Field>
                    <Rows rows={[[t('spa.redirect'), <code key="r">{`${(publicURL.trim() || window.location.origin).replace(/\/+$/, '')}/api/auth/spa/callback`}</code>]]} />
                    <Banner kind="info" title={t('spa.admin')} />
                    {spaCheck && spaCheck.key === spaCheckKey && (
                      <Banner kind={spaCheck.ok ? 'ok' : 'error'} title={spaCheck.ok ? t('spa.ok', { app: spaCheck.result?.application || 'Umbrella' }) : t('spa.fail')}>
                        {spaCheck.ok ? spaCheck.result?.issuer : spaCheck.error}
                      </Banner>
                    )}
                    {errorBanner}
                    <Foot back={back} next={next} canNext={canNext.spa} t={t} check={testSpa} busy={busy} checkDisabled={!spaKey.trim()} />
                  </div>
                )}

                {step === 'review' && (
                  <div className="stack">
                    <Head refEl={heading} title={t('rv.title')} text={t('rv.text')} />
                    <div className="summary">
                      <Summary
                        title={t('step.prefs')}
                        edit={() => setStep('prefs')}
                        t={t}
                        rows={[
                          [t('rv.locale'), t(`lang.${defaults.locale}`)],
                          [t('rv.theme'), t(`theme.${defaults.theme}`)],
                          [t('rv.timezone'), zoneLabel(defaults.timezone)],
                          [t('rv.public'), publicURL.trim() ? <code key="p">{publicURL.trim().replace(/\/+$/, '')}</code> : t('rv.public.none')],
                        ]}
                      />
                      <Summary
                        title="OpenBao"
                        edit={() => setStep('openbao')}
                        t={t}
                        rows={[
                          [t('rv.address'), <code key="a">{ob.addr.trim()}</code>],
                          [t('ob.mount'), ob.mount.trim()],
                          [t('rv.auth'), t(`ob.auth.${ob.auth}`)],
                        ]}
                      />
                      <Summary
                        title="PostgreSQL"
                        edit={() => setStep('postgres')}
                        t={t}
                        rows={[
                          [t('rv.database'), <code key="d">{`${pg.user.trim()}@${pg.host.trim()}:${pg.port}/${pg.database.trim()}`}</code>],
                          [t('pg.sslmode'), pg.sslmode],
                          ...(pgNeedsReuse ? ([[t('rv.reuse'), t('yes')]] as [ReactNode, ReactNode][]) : []),
                        ]}
                      />
                      <Summary
                        title="SPA"
                        edit={() => setStep('spa')}
                        t={t}
                        rows={[[t('spa.key'), spaKey.trim() ? t('spa.key.set') : t('spa.key.env')]]}
                      />
                    </div>
                    {error && (
                      <Banner kind="error" title={`${t('rv.failed')}: ${error.message}`}>
                        {error.detail && <div>{error.detail}</div>}
                        {error.step && (
                          <Button variant="ghost" onClick={() => setStep(error.step!)} style={{ marginTop: 8, paddingLeft: 0 }}>
                            {t('rv.goto')}: {t(`step.${error.step}`)}
                          </Button>
                        )}
                      </Banner>
                    )}
                    <div className="panel-foot">
                      <Button variant="ghost" onClick={back}>
                        {t('back')}
                      </Button>
                      <Button
                        variant="primary"
                        onClick={finish}
                        busy={busy}
                        disabled={!(publicValid && obValid && pgValid && (!pgNeedsReuse || pg.reuse_existing) && spaValid)}
                      >
                        {busy ? t('rv.finishing') : t('rv.finish')}
                      </Button>
                    </div>
                  </div>
                )}
              </motion.div>
            </AnimatePresence>
          </main>
        </div>
      </div>
      <Toaster />
    </MotionConfig>
  )
}

function Head({ title, text, refEl }: { title: string; text: string; refEl: RefObject<HTMLHeadingElement | null> }) {
  return (
    <div className="panel-head">
      <h1 ref={refEl} tabIndex={-1} style={{ outline: 'none' }}>
        {title}
      </h1>
      <p className="muted">{text}</p>
    </div>
  )
}

function Foot({
  back,
  next,
  canNext,
  t,
  check,
  busy,
  checkDisabled,
  submit,
}: {
  back: () => void
  next: () => void
  canNext: boolean
  t: (k: string) => string
  check?: () => void
  busy?: boolean
  checkDisabled?: boolean
  submit?: boolean
}) {
  return (
    <div className="panel-foot">
      <Button variant="ghost" onClick={back}>
        {t('back')}
      </Button>
      <div className="row">
        {check && (
          <Button onClick={check} busy={busy} disabled={checkDisabled}>
            {busy ? t('checking') : t('check')}
          </Button>
        )}
        <Button variant="primary" type={submit ? 'submit' : 'button'} onClick={submit ? undefined : next} disabled={!canNext}>
          {t('next')}
        </Button>
      </div>
    </div>
  )
}

function Summary({ title, rows, edit, t }: { title: string; rows: [ReactNode, ReactNode][]; edit: () => void; t: (k: string) => string }) {
  return (
    <section className="section">
      <header>
        <h3>{title}</h3>
        <Button variant="ghost" onClick={edit}>
          {t('rv.edit')}
        </Button>
      </header>
      <Rows rows={rows} />
    </section>
  )
}
