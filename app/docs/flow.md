# Интеграция с Umbrella Flow

Umbrella Flow — соседнее приложение для анализа трафика по NetFlow, IPFIX и sFlow (в духе Akvorado и
ElastiFlow). Интеграция двусторонняя и необязательная:

- **Flow → Umbrella.** Детектор Flow отправляет события трафика (загрузка каналов, DDoS, аномалии,
  молчащие экспортёры) в коннектор из шаблона `network-flow`; Umbrella делает из них инциденты и ведёт
  их как обычно: схлопывание, сервисные окна, маршрут, PagerDuty, резервное оповещение.
- **Umbrella → Flow.** Карточки инцидента и КЕ получают ссылку «Трафик» на консоль Flow с фильтром по
  КЕ и окном вокруг инцидента.

```mermaid
sequenceDiagram
  participant F as Umbrella Flow
  participant U as Umbrella
  participant P as Дежурный
  F->>U: POST /api/ingest/<адрес> (Bearer-токен), события firing / resolved
  U->>U: КЕ по source_ref, имени или IP; инцидент, маршрут, PagerDuty
  P->>U: карточка инцидента → «Трафик»
  U-->>P: 302 на <адрес Flow>/go/incident?ci=…&ip=…&ref=…&from=…&to=…
```

## Flow → Umbrella: события

### Подключение

1. В Umbrella: «Коннекторы» (или «Системы мониторинга») → **Подключить источник** → «Umbrella Flow» →
   «Подключить». Umbrella создаст Bearer-токен, коннектор из шаблона `network-flow` и опубликует его.
2. Скопируйте адрес приёма и токен (токен показывается один раз) в настройки Flow (раздел «Umbrella»);
   Flow хранит токен в OpenBao.
3. Кнопка «Отправить тестовое событие» проверит путь до инцидента и покажет маршрут.

То же через API: `POST /api/connectors/quick` с телом `{"preset": "network-flow"}` (права `connectors:edit`,
`connectors:publish`, `credentials:edit`), см. [connectors.md](connectors.md#быстрое-подключение-источника).

### Формат события

Один объект или массив объектов:

```json
{
  "id": "flow-7f3a1c",
  "status": "firing",
  "signal": "use.link.utilization",
  "method": "USE",
  "severity": "warning",
  "title": "Загрузка канала xe-0/0/1 на edge-01 — 93.5 %",
  "ci": "edge-01",
  "source_ref": "inventory-db:dcim.device:100",
  "ip": "10.0.0.1",
  "value": 93.5,
  "labels": {"interface": "xe-0/0/1", "direction": "in"},
  "links": {"flow": "https://flow.example.com/visualize?exporter=10.0.0.1&if=xe-0/0/1"},
  "starts_at": "2026-10-12T09:00:00Z",
  "ends_at": ""
}
```

| Поле Flow | Поле события Umbrella | Примечание |
| --- | --- | --- |
| `id` | внешний ID, ключ дедупликации | resolved с тем же `id` закрывает событие firing |
| `status` | статус | `firing` или `resolved`; пусто — firing |
| `signal` | сигнал | `use.link.utilization`, `use.link.commit`, `red.ddos.volumetric`, `red.ddos.syn`, `red.traffic.anomaly`, `red.traffic.drop`, `use.exporter.silent`, `sec.threat.contact` и другие |
| `method` | метод RED / USE | без поля — по префиксу сигнала `red.` / `use.`; иначе «другое» |
| `severity` | важность | `critical` (P1), `error` (P2), `warning` (P3), `low` (P4), `info` (P5) и синонимы; неизвестное — P3 |
| `title` | заголовок | пусто — сигнал |
| `ci`, затем `ip`, затем `source_ref` | имя КЕ в событии | см. «Как находится КЕ» |
| `value` | значение | |
| `labels` | метки | сохраняются как есть |
| `source_ref`, `ip`, `links.flow` | метки `source_ref`, `ip`, `flow_url` | плюс метка `source=network-flow` |
| `links.flow`, `ip`, `source_ref`, `starts_at`, `ends_at` | поля инцидента | «Трафик в Umbrella Flow», «IP», «Inventory DB», «Начало», «Окончание»; пустые не показываются |

### Как находится КЕ

1. Метка `source_ref` (из поля `source_ref`) совпадает со ссылкой Inventory DB ровно одной КЕ — берётся
   она. Ссылка есть у КЕ, загруженных из Inventory DB ([inventory-db.md](inventory-db.md)).
2. Иначе — обычный поиск по имени из поля `ci` (ID, имя, короткое имя, IP, DNS-имя, псевдонимы, ручные
   привязки хостов).
3. Имя не нашло КЕ (или нашло несколько) — метка `ip`, если это IP-адрес ровно одной КЕ.

Правила 1 и 3 работают для любого коннектора, который ставит метки `source_ref` и `ip`. Инцидент без
КЕ ждёт её, как и другие: КЕ появилась (загрузка Inventory DB, создание из карточки) — инцидент к ней
привязывается.

## Umbrella → Flow: ссылка «Трафик»

«Настройки → Оповещения» → карточка **Адрес Umbrella Flow** (рядом с Grafana): публичный адрес Flow,
например `https://flow.example.com`. Пока адрес не задан, ссылок нет нигде.

С адресом:

- карточка инцидента — кнопка «Трафик» на `/go/incidents/{id}/flow`;
- карточка КЕ — строка «Трафик» на `/go/cis/{id}/flow`;
- PagerDuty (ссылка «Traffic in Umbrella Flow») и резервное оповещение (письмо, Telegram, Teams, Zoom —
  ссылка «Трафик») — через публичный адрес Umbrella, как ссылка на Grafana.

Umbrella отвечает перенаправлением на консоль Flow:

```
<адрес Flow>/go/incident?ci=<имя КЕ>&ip=<IP>&ref=<source_ref>&from=<RFC 3339>&to=<RFC 3339>
```

| Параметр | Инцидент | КЕ |
| --- | --- | --- |
| `ci` | имя КЕ из каталога, без КЕ — имя из события | имя КЕ |
| `ip` | первый адрес КЕ, без него — метка `ip` события | первый адрес КЕ |
| `ref` | ссылка Inventory DB КЕ, без неё — метка `source_ref` события | ссылка Inventory DB КЕ |
| `from` | за 30 минут до открытия инцидента | час назад |
| `to` | сейчас; у решённого — 10 минут после решения (не позже текущего момента) | сейчас |

Пустые `ip` и `ref` не передаются. Доступ — как у ссылки на Grafana: вошедший пользователь с правом
`incidents:view` и инцидентом в своей области видимости (для КЕ — право `cis:view`) уходит во Flow,
остальные — на инцидент (`/incidents?id=…`) или КЕ (`/cis?id=…`) в Umbrella. Переход из PagerDuty, почты
или мессенджера приходит без cookie сессии (SameSite=Strict): Umbrella отдаёт короткую страницу,
которая повторяет запрос уже со своего адреса.

## API

| Метод | Путь | Право | Назначение |
| --- | --- | --- | --- |
| GET | `/api/flow` | `settings.alerting:view` | `{url, updated_at, updated_by}` |
| PUT | `/api/flow` | `settings.alerting:edit` | `{url}`: http или https без параметров, конечный `/` отбрасывается; пусто — выключить ссылки; ошибка `url_invalid` |
| GET | `/go/incidents/{id}/flow` | вход + `incidents:view` | перенаправление на трафик инцидента |
| GET | `/go/cis/{id}/flow` | вход + `cis:view` | перенаправление на трафик КЕ за последний час |
| POST | `/api/connectors/quick` `{"preset":"network-flow"}` | `connectors:edit`, `connectors:publish`, `credentials:edit` | быстрое подключение Flow |

Ответы `GET /api/incidents/{id}` и КЕ (`GET /api/cis`, `GET /api/cis/{id}`) содержат `flow_url` (путь
`/go/…/flow`), когда адрес Flow задан. Изменение адреса пишется в журнал аудита (`settings.flow`).
