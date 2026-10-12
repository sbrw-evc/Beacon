import type { Dict } from '../../i18n'

export const tokenStrings: Dict = {
  en: {
    'tk.create': 'Create token',
    'tk.name': '{name} token',
    'tk.default': 'Connector',
    'tk.made': 'Token «{name}» is created and selected',
    'tk.once': 'Copy it now: it is shown only once and is kept in OpenBao. The source sends it as «Authorization: Bearer <token>».',
    'tk.copy': 'Copy',
    'tk.copied': 'Copied',
  },
  ru: {
    'tk.create': 'Создать токен',
    'tk.name': 'Токен {name}',
    'tk.default': 'коннектора',
    'tk.made': 'Токен «{name}» создан и выбран',
    'tk.once': 'Скопируйте его сейчас: он показывается один раз и хранится в OpenBao. Источник передаёт его в заголовке «Authorization: Bearer <токен>».',
    'tk.copy': 'Скопировать',
    'tk.copied': 'Скопировано',
  },
}
