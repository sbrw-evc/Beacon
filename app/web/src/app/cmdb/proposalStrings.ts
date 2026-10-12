import type { Dict } from '../../i18n'
import { strings } from './strings'

const own: Dict = {
  en: {
    'pr.title': 'Proposed dependencies ({n})',
    'pr.hint': 'Umbrella Flow sees steady traffic between these configuration items. Accepting creates the dependency between their business services; a rejected proposal stays hidden unless its traffic grows ten times. Proposals Flow stops sending disappear after 14 days.',
    'pr.dep': '{from} depends on {to}',
    'pr.exists': 'already set',
    'pr.noServices': 'The items belong to no business services yet: bind them to services to accept.',
    'pr.unknownPort': 'port not detected',
    'pr.volume': '{bytes}, {flows} flows in 7 days',
    'pr.days': 'seen on {n} of 7 days',
    'pr.lastSeen': 'last {at}',
    'pr.flow': 'Traffic in Flow',
    'pr.accept': 'Accept',
    'pr.accept.none': 'The dependencies are already set or the items have no services.',
    'pr.reject': 'Reject',
    'pr.reject.hint': 'Hide this proposal until its traffic grows ten times',
    'err.proposal_unresolved': 'The items belong to no business services, so there is no dependency to create.',
    'err.dependency_cycle': 'The dependency would create a cycle between services.',
    'err.not_found': 'The proposal no longer exists. Refresh the map.',
  },
  ru: {
    'pr.title': 'Предлагаемые зависимости ({n})',
    'pr.hint': 'Umbrella Flow видит устойчивый трафик между этими КЕ. Принятие создаёт зависимость между их бизнес-сервисами; отклонённое предложение скрыто, пока трафик не вырастет в десять раз. Предложения, которые Flow перестал присылать, исчезают через 14 дней.',
    'pr.dep': '{from} зависит от {to}',
    'pr.exists': 'уже задана',
    'pr.noServices': 'КЕ пока не входят в бизнес-сервисы: привяжите их к сервисам, чтобы принять.',
    'pr.unknownPort': 'порт не определён',
    'pr.volume': '{bytes}, потоков за 7 дней: {flows}',
    'pr.days': 'виден {n} дн. из 7',
    'pr.lastSeen': 'последний раз {at}',
    'pr.flow': 'Трафик во Flow',
    'pr.accept': 'Принять',
    'pr.accept.none': 'Зависимости уже заданы или у КЕ нет сервисов.',
    'pr.reject': 'Отклонить',
    'pr.reject.hint': 'Скрыть предложение, пока трафик не вырастет в десять раз',
    'err.proposal_unresolved': 'КЕ не входят в бизнес-сервисы, создавать нечего.',
    'err.dependency_cycle': 'Зависимость создала бы цикл между сервисами.',
    'err.not_found': 'Предложения больше нет. Обновите карту.',
  },
}

export const proposalStrings: Dict = { en: { ...strings.en, ...own.en }, ru: { ...strings.ru, ...own.ru } }
