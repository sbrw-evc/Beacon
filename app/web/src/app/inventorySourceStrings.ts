import type { Dict } from '../i18n'

// Texts of the inventory source notices (inventorySource.tsx).
export const sourceStrings: Dict = {
  en: {
    'src.ledger.title': 'Ledger is the inventory source',
    'src.ledger.netbox':
      'Beacon reads devices from one inventory source. While Ledger is connected, the NetBox synchronization cannot be turned on: it is for sites that run a real NetBox instead of Ledger.',
    'src.ledger.paused':
      'The NetBox synchronization was paused on {at} because Ledger was connected too. Its settings, token and the items it imported are kept; Beacon writes nothing to NetBox while Ledger is the source.',
    'src.netbox.title': 'NetBox is the inventory source',
    'src.netbox.ledger': 'Beacon reads devices from one inventory source. Turn the NetBox synchronization off before connecting Ledger.',
    'src.open.ledger': 'Open the Ledger connection',
    'src.open.netbox': 'Open the NetBox synchronization',
    'src.status.title': 'NetBox synchronization paused',
    'src.create.ledger': 'Create the device in Ledger',
    'src.create.ledger.hint':
      'Devices are kept in Ledger. Create the device there; once Beacon reads it (on the next Ledger synchronization), it appears here as a configuration item.',
    'src.create.ledger.bind': 'Once Beacon reads it from Ledger, bind the incident to it with “Bind to an existing item”.',
  },
  ru: {
    'src.ledger.title': 'Источник инвентаря — Ledger',
    'src.ledger.netbox':
      'Beacon читает устройства из одного источника инвентаря. Пока подключён Ledger, синхронизацию с NetBox включить нельзя: она для площадок, где вместо Ledger работает настоящий NetBox.',
    'src.ledger.paused':
      'Синхронизация с NetBox приостановлена {at}, потому что был подключён и Ledger. Её настройки, токен и импортированные КЕ сохранены; пока источник — Ledger, Beacon ничего не пишет в NetBox.',
    'src.netbox.title': 'Источник инвентаря — NetBox',
    'src.netbox.ledger': 'Beacon читает устройства из одного источника инвентаря. Выключите синхронизацию с NetBox, прежде чем подключать Ledger.',
    'src.open.ledger': 'Открыть подключение Ledger',
    'src.open.netbox': 'Открыть синхронизацию с NetBox',
    'src.status.title': 'Синхронизация с NetBox приостановлена',
    'src.create.ledger': 'Создать устройство в Ledger',
    'src.create.ledger.hint':
      'Устройства ведутся в Ledger. Создайте устройство там; когда Beacon прочитает его (при следующей синхронизации с Ledger), оно появится здесь как КЕ.',
    'src.create.ledger.bind': 'Когда Beacon прочитает его из Ledger, привяжите инцидент к нему через «Привязать к существующей КЕ».',
  },
}
