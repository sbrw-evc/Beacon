import type { Dict } from '../../../i18n'
import { policyEditorStrings } from '../../../policyEditorStrings'

const page: Dict = {
  en: {
    'ps.title': 'Password policy for local accounts',
    'ps.text': 'Rules for passwords of local Umbrella accounts. Directory (LDAP / AD) accounts follow the directory rules.',
    'ps.applies':
      'New rules apply when a password is set or changed. A shorter lifetime takes effect at once: users whose password is older must change it at the next sign-in.',
    'ps.accounts': 'Local accounts',
    'ps.expired': 'Passwords expired',
    'ps.expiring': 'Passwords expiring soon',
    'ps.rules': 'Rules',
    'ps.preview': 'Try a password',
    'ps.preview.text': 'Type a sample password to see how the rules above judge it. Nothing is saved.',
    'ps.sample': 'Sample password',
    'ps.save': 'Save policy',
    'ps.reset': 'Discard changes',
    'ps.saved': 'Password policy saved.',
  },
  ru: {
    'ps.title': 'Парольная политика локальных учётных записей',
    'ps.text': 'Правила для паролей локальных учётных записей Umbrella. Учётные записи каталога (LDAP / AD) подчиняются правилам каталога.',
    'ps.applies':
      'Новые правила применяются, когда пароль задают или меняют. Сокращение срока действия работает сразу: пользователи с более старым паролем должны сменить его при следующем входе.',
    'ps.accounts': 'Локальные учётные записи',
    'ps.expired': 'Пароль истёк',
    'ps.expiring': 'Пароль скоро истечёт',
    'ps.rules': 'Правила',
    'ps.preview': 'Проверка пароля',
    'ps.preview.text': 'Введите пример пароля, чтобы увидеть, как его оценят правила выше. Ничего не сохраняется.',
    'ps.sample': 'Пример пароля',
    'ps.save': 'Сохранить политику',
    'ps.reset': 'Отменить изменения',
    'ps.saved': 'Парольная политика сохранена.',
  },
}

export const strings: Dict = { en: { ...policyEditorStrings.en, ...page.en }, ru: { ...policyEditorStrings.ru, ...page.ru } }
