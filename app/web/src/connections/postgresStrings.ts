import type { Dict } from '../i18n'

export const postgresStrings: Dict = {
  en: {
    'pg.host': 'Host',
    'pg.port': 'Port',
    'pg.database': 'Database',
    'pg.user': 'User',
    'pg.password': 'Password',
    'pg.sslmode': 'SSL mode',
    'pg.ok': 'PostgreSQL is reachable',
    'pg.ok.text': 'PostgreSQL {version}, database {database}, user {user}.',
    'pg.fail': 'PostgreSQL check failed',
    'pg.loopback':
      'Umbrella runs in a container, so localhost is the Umbrella container itself. Use the PostgreSQL service name in the shared Docker network, for example postgres, or host.docker.internal when PostgreSQL runs on the host.',
    'pg.nocreate': 'The user cannot create tables in this database. Grant CREATE on the schema or pick another user.',
    'pg.state': 'This database already holds Umbrella data (saved {at})',
    'pg.state.data': 'This database already holds Umbrella incidents or received requests',
  },
  ru: {
    'pg.host': 'Хост',
    'pg.port': 'Порт',
    'pg.database': 'База данных',
    'pg.user': 'Пользователь',
    'pg.password': 'Пароль',
    'pg.sslmode': 'Режим SSL',
    'pg.ok': 'PostgreSQL доступен',
    'pg.ok.text': 'PostgreSQL {version}, база {database}, пользователь {user}.',
    'pg.fail': 'Проверка PostgreSQL не прошла',
    'pg.loopback':
      'Umbrella работает в контейнере, и localhost — это сам контейнер Umbrella. Укажите имя сервиса PostgreSQL в общей сети Docker, например postgres, или host.docker.internal, если PostgreSQL запущен на хосте.',
    'pg.nocreate': 'У пользователя нет права создавать таблицы в этой базе. Выдайте CREATE на схему или выберите другого пользователя.',
    'pg.state': 'В базе уже есть данные Umbrella (сохранены {at})',
    'pg.state.data': 'В базе уже есть инциденты или принятые запросы Umbrella',
  },
}
