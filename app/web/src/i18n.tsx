import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { animate } from 'motion/react'
import { ApiError, type Locale } from './api'
import { reducedMotion } from './fx'

export type Dict = Record<Locale, Record<string, string>>

const KEY = 'umbrella.locale'

export const common: Dict = {
  en: {
    'num.dec': 'Decrease',
    'num.inc': 'Increase',
    'app.name': 'Beacon',
    'theme.light': 'Light',
    'theme.dark': 'Dark',
    'theme.toggle': 'Switch theme',
    'lang.en': 'English',
    'lang.ru': 'Русский',
    'lang.toggle': 'Language',
    yes: 'Yes',
    no: 'No',
    'sev.critical': 'Critical',
    'sev.error': 'High',
    'sev.warning': 'Medium',
    'sev.low': 'Low',
    'sev.info': 'Informational',
    loading: 'Loading…',
    retry: 'Retry',
    'err.network': 'The server does not answer. Check the connection and try again.',
    'err.bad_request': 'The request was rejected as invalid.',
    'err.internal': 'Internal server error.',
    'err.invalid_setup_token': 'The setup code is wrong.',
    'err.too_many_attempts': 'Too many failed attempts. Wait a few minutes and try again.',
    'err.setup_completed': 'Setup is already completed. Reload the page.',
    'err.setup_required': 'Initial setup is not finished yet.',
    'err.invalid_email': 'The e-mail address is invalid.',
    'err.invalid_name': 'The display name is too long.',
    'err.invalid_locale': 'Choose English or Russian.',
    'err.invalid_theme': 'Choose the light or dark theme.',
    'err.invalid_timezone': 'Unknown time zone.',
    'err.spa_key_invalid': 'This is not a Keystone connection key (spa1.…): copy it again from Keystone → Applications.',
    'err.managed_in_spa': 'This is managed in Keystone.',
    'err.still_in_spa': 'The person still has access in Keystone. Take the role away in Keystone first.',
    'err.role_in_use': 'The role is still given to people in Keystone. Take it away there first.',
    'err.unknown_owner': 'Choose a person as the owner.',
    'err.openbao_invalid': 'OpenBao settings are incomplete.',
    'err.openbao_unavailable': 'Beacon cannot work with OpenBao using these settings.',
    'err.openbao_write_failed': 'Beacon could not save secrets to OpenBao.',
    'err.ldap_invalid': 'LDAP / AD settings are incomplete.',
    'err.ldap_unavailable': 'Beacon cannot connect to the directory using these settings.',
    'err.postgres_invalid': 'PostgreSQL settings are incomplete.',
    'err.postgres_unavailable': 'Beacon cannot connect to PostgreSQL.',
    'err.postgres_has_state': 'The database already holds Beacon data. Confirm that it should be used.',
    'err.postgres_no_create': 'The database user cannot create tables in the database.',
    'err.postgres_state_unreadable': 'The database holds Beacon data that cannot be read.',
    'err.postgres_write_failed': 'Beacon could not write to PostgreSQL.',
    'err.config_write_failed': 'Beacon could not write its configuration file.',
    'err.unauthenticated': 'Your session has ended. Sign in again.',
    'err.forbidden': 'Not enough permissions.',
    'err.csrf': 'The session token is outdated. Reload the page.',
    'err.avatar_invalid': 'The file is not a supported image (PNG, JPEG, GIF or WebP).',
    'err.avatar_too_large': 'The image is larger than 5 MB.',
    'err.secrets_unavailable': 'The secret store (OpenBao) is unavailable. Try again later.',
    'err.unknown': 'Something went wrong.',
  },
  ru: {
    'num.dec': 'Уменьшить',
    'num.inc': 'Увеличить',
    'app.name': 'Beacon',
    'theme.light': 'Светлая',
    'theme.dark': 'Тёмная',
    'theme.toggle': 'Сменить тему',
    'lang.en': 'English',
    'lang.ru': 'Русский',
    'lang.toggle': 'Язык',
    yes: 'Да',
    no: 'Нет',
    'sev.critical': 'Критический',
    'sev.error': 'Высокий',
    'sev.warning': 'Средний',
    'sev.low': 'Низкий',
    'sev.info': 'Информационный',
    loading: 'Загрузка…',
    retry: 'Повторить',
    'err.network': 'Сервер не отвечает. Проверьте подключение и повторите.',
    'err.bad_request': 'Запрос отклонён как некорректный.',
    'err.internal': 'Внутренняя ошибка сервера.',
    'err.invalid_setup_token': 'Неверный код установки.',
    'err.too_many_attempts': 'Слишком много неудачных попыток. Подождите несколько минут.',
    'err.setup_completed': 'Настройка уже завершена. Обновите страницу.',
    'err.setup_required': 'Первичная настройка ещё не завершена.',
    'err.invalid_email': 'Некорректный адрес почты.',
    'err.invalid_name': 'Слишком длинное имя.',
    'err.invalid_locale': 'Выберите английский или русский язык.',
    'err.invalid_theme': 'Выберите светлую или тёмную тему.',
    'err.spa_key_invalid': 'Это не ключ подключения Keystone (spa1.…): скопируйте его снова в Keystone → Приложения.',
    'err.managed_in_spa': 'Это настраивается в Keystone.',
    'err.still_in_spa': 'У человека ещё есть доступ в Keystone. Сначала заберите роль в Keystone.',
    'err.role_in_use': 'Роль ещё выдана людям в Keystone. Сначала заберите её там.',
    'err.unknown_owner': 'Выберите владельца — человека.',
    'err.invalid_timezone': 'Неизвестный часовой пояс.',
    'err.openbao_invalid': 'Настройки OpenBao заполнены не полностью.',
    'err.openbao_unavailable': 'С этими настройками Beacon не может работать с OpenBao.',
    'err.openbao_write_failed': 'Beacon не смог сохранить секреты в OpenBao.',
    'err.ldap_invalid': 'Настройки LDAP / AD заполнены не полностью.',
    'err.ldap_unavailable': 'С этими настройками Beacon не может подключиться к каталогу.',
    'err.postgres_invalid': 'Настройки PostgreSQL заполнены не полностью.',
    'err.postgres_unavailable': 'Beacon не может подключиться к PostgreSQL.',
    'err.postgres_has_state': 'В базе уже есть данные Beacon. Подтвердите, что их нужно использовать.',
    'err.postgres_no_create': 'У пользователя БД нет права создавать таблицы.',
    'err.postgres_state_unreadable': 'В базе есть данные Beacon, но прочитать их нельзя.',
    'err.postgres_write_failed': 'Beacon не смог записать данные в PostgreSQL.',
    'err.config_write_failed': 'Beacon не смог записать файл конфигурации.',
    'err.unauthenticated': 'Сессия завершилась. Войдите снова.',
    'err.forbidden': 'Недостаточно прав.',
    'err.csrf': 'Токен сессии устарел. Обновите страницу.',
    'err.avatar_invalid': 'Файл не является поддерживаемым изображением (PNG, JPEG, GIF или WebP).',
    'err.avatar_too_large': 'Изображение больше 5 МБ.',
    'err.secrets_unavailable': 'Хранилище секретов (OpenBao) недоступно. Повторите позже.',
    'err.unknown': 'Что-то пошло не так.',
  },
}

type Ctx = { locale: Locale; setLocale: (l: Locale, persist?: boolean) => void }

const LocaleContext = createContext<Ctx>({ locale: 'en', setLocale: () => {} })

export function savedLocale(): Locale | null {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'en' || v === 'ru' ? v : null
  } catch {
    return null
  }
}

export function browserLocale(): Locale {
  return navigator.language?.toLowerCase().startsWith('ru') ? 'ru' : 'en'
}

export function LocaleProvider({ initial, children }: { initial: Locale; children: ReactNode }) {
  const [locale, set] = useState<Locale>(initial)
  const current = useRef(initial)
  const busy = useRef(false)
  const setLocale = useCallback((l: Locale, persist = true) => {
    const apply = () => {
      current.current = l
      flushSync(() => set(l))
      document.documentElement.lang = l
    }
    const root = document.getElementById('root')
    if (l === current.current || !root || reducedMotion() || busy.current) {
      apply()
    } else {
      busy.current = true
      animate(root, { opacity: 0, filter: 'blur(3px)' }, { duration: 0.14, ease: 'easeIn' })
        .then(() => {
          apply()
          return animate(root, { opacity: 1, filter: 'blur(0px)' }, { duration: 0.24, ease: [0.22, 1, 0.36, 1] })
        })
        .finally(() => {
          root.style.filter = ''
          busy.current = false
        })
    }
    if (!persist) return
    try {
      localStorage.setItem(KEY, l)
    } catch {
      return
    }
  }, [])
  document.documentElement.lang = locale
  return <LocaleContext.Provider value={{ locale, setLocale }}>{children}</LocaleContext.Provider>
}

export function useLocale() {
  return useContext(LocaleContext)
}

export function useT(extra?: Dict) {
  const { locale } = useLocale()
  return useMemo(() => {
    const table = { ...common[locale], ...(extra?.[locale] ?? {}) }
    return (key: string, vars?: Record<string, string | number>) => {
      let s = table[key] ?? key
      if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v))
      return s
    }
  }, [locale, extra])
}

export function errorText(t: (k: string) => string, e: unknown): { message: string; detail?: string } {
  if (e instanceof ApiError) {
    const key = `err.${e.code}`
    const message = t(key)
    return { message: message === key ? t('err.unknown') : message, detail: e.detail }
  }
  return { message: t('err.unknown'), detail: e instanceof Error ? e.message : String(e) }
}
