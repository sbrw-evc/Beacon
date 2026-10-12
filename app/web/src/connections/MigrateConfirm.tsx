import type { ReactNode } from 'react'
import { useT } from '../i18n'
import { Button, Modal } from '../ui'
import { connectionStrings } from './connectionStrings'

export function MigrateConfirm({
  open,
  title,
  busy,
  onClose,
  onConfirm,
  children,
}: {
  open: boolean
  title: string
  busy: boolean
  onClose: () => void
  onConfirm: () => void
  children: ReactNode
}) {
  const t = useT(connectionStrings)
  return (
    <Modal
      open={open}
      title={title}
      onClose={() => !busy && onClose()}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            {t('conn.cancel')}
          </Button>
          <Button variant="primary" onClick={onConfirm} busy={busy}>
            {t('conn.confirm')}
          </Button>
        </>
      }
    >
      {children}
    </Modal>
  )
}
