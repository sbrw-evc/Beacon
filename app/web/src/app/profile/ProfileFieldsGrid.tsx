import { useT } from '../../i18n'
import { Field, Input } from '../../ui'
import { strings } from '../strings'
import { PROFILE_KEYS, type ProfileFields } from '../types'

const REQUIRED: (keyof ProfileFields)[] = ['last_name', 'first_name']

export function ProfileFieldsGrid({ value, onChange, editable }: { value: ProfileFields; onChange: (v: ProfileFields) => void; editable: boolean }) {
  const t = useT(strings)
  return (
    <div className="grid-2">
      {PROFILE_KEYS.map((k) => (
        <Field key={k} label={t(`field.${k}`)} optional={editable && !REQUIRED.includes(k) ? t('optional') : undefined}>
          {(id) => (
            <Input
              id={id}
              type={k === 'email' ? 'email' : 'text'}
              value={value[k]}
              disabled={!editable}
              readOnly={!editable}
              onChange={(e) => onChange({ ...value, [k]: e.target.value })}
            />
          )}
        </Field>
      ))}
    </div>
  )
}
