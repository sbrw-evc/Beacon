import type { Dict } from '../i18n'

export const connectionStrings: Dict = {
  en: {
    'conn.state.ok': 'Working',
    'conn.state.error': 'Error',
    'conn.test': 'Test connection',
    'conn.check': 'Check',
    'conn.refresh': 'Refresh',
    'conn.cancel': 'Cancel',
    'conn.migrate': 'Move data',
    'conn.confirm': 'I understand, move the data',
    'conn.done': 'Done',
    'err.switching': 'Umbrella is switching its connections. Try again in a moment.',
    'err.switch_unavailable': 'Connections cannot be switched in this mode.',
  },
  ru: {
    'conn.state.ok': 'Работает',
    'conn.state.error': 'Ошибка',
    'conn.test': 'Проверить подключение',
    'conn.check': 'Проверить',
    'conn.refresh': 'Обновить',
    'conn.cancel': 'Отмена',
    'conn.migrate': 'Перенести данные',
    'conn.confirm': 'Понятно, перенести данные',
    'conn.done': 'Готово',
    'err.switching': 'Umbrella переключает подключения. Повторите через несколько секунд.',
    'err.switch_unavailable': 'В этом режиме переключить подключения нельзя.',
  },
}

export function mergeDicts(...dicts: Dict[]): Dict {
  return {
    en: Object.assign({}, ...dicts.map((d) => d.en)),
    ru: Object.assign({}, ...dicts.map((d) => d.ru)),
  }
}
