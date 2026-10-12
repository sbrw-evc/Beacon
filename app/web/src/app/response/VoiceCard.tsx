import { useEffect, useState } from 'react'
import { Play } from 'lucide-react'
import { api } from '../../api'
import { useT } from '../../i18n'
import { Banner, Button, Field, Input, Password, Segmented, Stepper, Switch, Textarea } from '../../ui'
import { ProfileCard } from '../profile/ProfileCard'
import { useAction } from '../profile/useAction'
import { useSession } from '../session'
import { responseStrings } from './strings'
import { VOICE_VIA, type Mode, type ResponseView, type TTSProvider } from './types'

const LANGS = ['ru', 'en'] as const
type Lang = (typeof LANGS)[number]

// VoiceCard is the voice settings: the speech server, the voices, the text of each language,
// listening to it and a test on oneself.
export function VoiceCard({ view, onSaved }: { view: ResponseView; onSaved: (v: ResponseView) => void }) {
  const t = useT(responseStrings)
  const { can } = useSession()
  const canEdit = can('response:edit')
  const v = view.voice
  const init = {
    mode: v.mode,
    provider: v.tts.provider,
    url: v.tts.url,
    voices: { ru: v.tts.voices.ru ?? '', en: v.tts.voices.en ?? '' },
    model: v.tts.model ?? '',
    api_key: '',
    templates: { ru: v.templates.ru || v.default_templates.ru, en: v.templates.en || v.default_templates.en },
    repeat: v.repeat,
    ack_digit: v.ack_digit,
  }
  const [d, setD] = useState(init)
  const saver = useAction(responseStrings)
  const tester = useAction(responseStrings)
  const dirty = JSON.stringify(d) !== JSON.stringify(init)
  const engineDirty = d.provider !== init.provider || d.url !== init.url || JSON.stringify(d.voices) !== JSON.stringify(init.voices) || d.model !== init.model || d.api_key !== ''
  const save = () =>
    saver.run(async () => {
      onSaved(await api<ResponseView>('PUT', '/api/response/voice', d))
      return t('rs.saved')
    })
  const test = (via: string) =>
    tester.run(async () => {
      await api('POST', '/api/response/voice/test', { via })
      return t('rs.voice.test.ok', { via: t(`via.${via}`) })
    })
  const list = v.placeholders.map((p) => `{${p}}`).join(', ')
  return (
    <ProfileCard
      title={t('rs.voice.title')}
      action={tester.error || tester.notice ? tester : saver}
      onSubmit={save}
      wide
      footer={
        <>
          {can('response:test') &&
            VOICE_VIA.map((via) => (
              <Button key={via} busy={tester.busy} disabled={dirty || v.mode === 'off'} onClick={() => void test(via)}>
                {t('rs.voice.test', { via: t(`via.${via}`) })}
              </Button>
            ))}
          {canEdit && (
            <Button type="submit" variant="primary" busy={saver.busy} disabled={!dirty}>
              {t('rs.save')}
            </Button>
          )}
        </>
      }
    >
      <p className="muted">{t('rs.voice.text')}</p>
      <p className="hint">{t('rs.voice.needs')}</p>
      <fieldset className="plain-fieldset" disabled={!canEdit}>
        <Segmented label={t('rs.int.mode')} value={d.mode} onChange={(mode: Mode) => setD({ ...d, mode })} options={(['off', 'dry_run', 'live'] as Mode[]).map((m) => ({ value: m, label: t(`rs.mode.${m}`) }))} />
        <Segmented
          label={t('rs.voice.engine')}
          value={d.provider}
          onChange={(provider: TTSProvider) => setD({ ...d, provider })}
          options={(['piper', 'rhvoice', 'openai'] as TTSProvider[]).map((p) => ({ value: p, label: t(`rs.voice.engine.${p}`) }))}
        />
        <p className="hint">{t(`rs.voice.engine.hint.${d.provider}`)}</p>
        {d.provider === 'piper' && !v.ffmpeg && <Banner kind="info" title={t('rs.voice.ffmpeg.off')} />}
        <div className="grid-2 rs-int-grid">
          <Field label={t('rs.voice.url')}>
            {(id) => (
              <Input
                id={id}
                value={d.url}
                placeholder={{ piper: 'http://piper:5000', rhvoice: 'http://rhvoice:8080', openai: 'http://tts:8000' }[d.provider]}
                onChange={(e) => setD({ ...d, url: e.target.value })}
              />
            )}
          </Field>
          {d.provider === 'openai' ? (
            <Field label={t('rs.voice.model')}>{(id) => <Input id={id} value={d.model} placeholder="tts-1" onChange={(e) => setD({ ...d, model: e.target.value })} />}</Field>
          ) : (
            <span />
          )}
          {LANGS.map((l) => (
            <Field key={l} label={t(`rs.voice.voice_${l}`)}>
              {(id) => <Input id={id} value={d.voices[l]} onChange={(e) => setD({ ...d, voices: { ...d.voices, [l]: e.target.value } })} />}
            </Field>
          ))}
          <Field label={t('rs.voice.key')} hint={[v.has_key ? t('rs.int.secret.set') : '', t('rs.voice.key.hint')].filter(Boolean).join(' ')}>
            {(id) => <Password id={id} value={d.api_key} autoComplete="new-password" onChange={(e) => setD({ ...d, api_key: e.target.value })} />}
          </Field>
          <Field label={t('rs.voice.repeat')}>{(id) => <Stepper id={id} value={d.repeat} min={1} max={3} onChange={(repeat) => setD({ ...d, repeat })} />}</Field>
        </div>
        <Switch checked={d.ack_digit} onChange={(ack_digit) => setD({ ...d, ack_digit })} label={t('rs.voice.ack')} />
        {LANGS.map((l) => (
          <VoiceTemplate
            key={l}
            lang={l}
            value={d.templates[l]}
            builtin={v.default_templates[l]}
            hint={t('rs.voice.tmpl.hint', { list })}
            listen={!engineDirty && v.mode !== 'off'}
            onChange={(text) => setD({ ...d, templates: { ...d.templates, [l]: text } })}
          />
        ))}
        {engineDirty && <p className="hint">{t('rs.voice.unsaved')}</p>}
      </fieldset>
    </ProfileCard>
  )
}

function VoiceTemplate({ lang, value, builtin, hint, listen, onChange }: { lang: Lang; value: string; builtin: string; hint: string; listen: boolean; onChange: (v: string) => void }) {
  const t = useT(responseStrings)
  const [sample, setSample] = useState('')
  const [audio, setAudio] = useState('')
  const query = `locale=${lang}&template=${encodeURIComponent(value)}`
  useEffect(() => {
    const timer = window.setTimeout(() => {
      api<{ text: string }>('GET', `/api/response/voice/preview?format=text&${query}`)
        .then((r) => setSample(r.text))
        .catch(() => setSample(''))
    }, 300)
    return () => window.clearTimeout(timer)
  }, [query])
  return (
    <div className="rs-voice-tmpl">
      <Field label={t('rs.voice.tmpl', { lang: t(`lang.${lang}`) })} hint={hint}>
        {(id) => <Textarea id={id} rows={4} maxLength={2000} value={value} onChange={(e) => onChange(e.target.value)} />}
      </Field>
      {sample && <p className="hint">{t('rs.voice.sample', { text: sample })}</p>}
      <div className="row">
        {value !== builtin && (
          <Button variant="ghost" onClick={() => onChange(builtin)}>
            {t('rs.voice.tmpl.reset')}
          </Button>
        )}
        {listen && (
          <Button onClick={() => setAudio(`/api/response/voice/preview?${query}&n=${Date.now()}`)}>
            <Play size={14} aria-hidden /> {t('rs.voice.listen')}
          </Button>
        )}
        {audio && <audio className="rs-voice-audio" src={audio} controls autoPlay />}
      </div>
    </div>
  )
}
