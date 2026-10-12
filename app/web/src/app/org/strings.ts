import type { Dict } from '../../i18n'

export const orgStrings: Dict = {
  en: {
    'org.expand': 'Expand',
    'org.collapse': 'Collapse',
    'org.locked': 'Locked',
    'org.source.local': 'local',
    'org.source.ldap': 'LDAP / AD',
    'org.source.entra': 'Entra ID',
    'org.picked': 'Selected: {n}',
    'org.cancel': 'Cancel',
    'org.search.users': 'Search by name or username',
    'org.picker.none': 'Everyone is already here.',
    'org.nothing': 'Nothing found.',
    'err.unknown_user': 'One of the selected users no longer exists. Reload the page.',
    'err.invalid_description': 'The description is longer than 1000 characters.',
    'err.last_admin': 'At least one active administrator must remain.',
    'err.own_admin': 'You cannot take the administrator role away from yourself.',
    'err.admin_only': 'Only administrators can grant or take away the administrator role.',
    'err.no_local_admin': 'At least one active local administrator must remain while directory sign-in is on.',
    'err.not_found': 'The object no longer exists. Reload the page.',
  },
  ru: {
    'org.expand': 'Развернуть',
    'org.collapse': 'Свернуть',
    'org.locked': 'Заблокирован',
    'org.source.local': 'локальный',
    'org.source.ldap': 'LDAP / AD',
    'org.source.entra': 'Entra ID',
    'org.picked': 'Выбрано: {n}',
    'org.cancel': 'Отмена',
    'org.search.users': 'Поиск по имени или логину',
    'org.picker.none': 'Все пользователи уже здесь.',
    'org.nothing': 'Ничего не найдено.',
    'err.unknown_user': 'Одного из выбранных пользователей больше нет. Обновите страницу.',
    'err.invalid_description': 'Описание длиннее 1000 символов.',
    'err.last_admin': 'Должен остаться хотя бы один активный администратор.',
    'err.own_admin': 'Нельзя снять роль администратора с самого себя.',
    'err.admin_only': 'Выдавать и снимать роль администратора могут только администраторы.',
    'err.no_local_admin': 'Пока включён вход через каталог, должен остаться хотя бы один активный локальный администратор.',
    'err.not_found': 'Объект больше не существует. Обновите страницу.',
  },
}

export function withOrg(page: Dict): Dict {
  return { en: { ...orgStrings.en, ...page.en }, ru: { ...orgStrings.ru, ...page.ru } }
}
