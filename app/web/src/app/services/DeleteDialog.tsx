import { api } from '../../api'
import { ErrorFlash } from '../../connections/ConnectionCard'
import { useAction } from '../../connections/useRequest'
import { useT } from '../../i18n'
import { Banner, Button, Modal } from '../../ui'
import { strings } from './strings'
import type { Service } from './types'

export function DeleteDialog({ service, onClose, onDeleted }: { service: Service | null; onClose: () => void; onDeleted: (s: Service) => void }) {
  const t = useT(strings)
  const remover = useAction()
  const close = () => {
    remover.clear()
    onClose()
  }
  const confirm = () =>
    service &&
    void remover.run(async () => {
      await api('DELETE', `/api/services/${service.id}`)
      onDeleted(service)
    })
  return (
    <Modal
      open={service !== null}
      title={t('svc.delete.title')}
      onClose={close}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            {t('svc.cancel')}
          </Button>
          <Button variant="danger" busy={remover.busy} onClick={confirm}>
            {t('svc.delete.confirm')}
          </Button>
        </>
      }
    >
      {service && (
        <>
          <p>{t('svc.delete.text', { name: service.name })}</p>
          {service.dependents.length > 0 && (
            <Banner kind="warn" title={t('svc.delete.dependents', { names: service.dependents.map((d) => d.name).join(', ') })} />
          )}
          <ErrorFlash error={remover.error} strings={strings} />
        </>
      )}
    </Modal>
  )
}
