import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { accountsMessages } from '@/features/accounts/messages'
import { ProxyInput } from '@/features/accounts/ProxyInput'
import { useMessages } from '@/lib/i18n'

// ProxyDialog 是填写账号固定代理的对话框，以 initial 为初值，留空表示从代理池分配，direct 表示直连
export function ProxyDialog({
  title,
  initial,
  pending,
  onSave,
  onClose,
}: {
  title: string
  initial: string
  pending: boolean
  onSave: (proxy: string) => void
  onClose: () => void
}) {
  const m = useMessages(accountsMessages)
  const [proxy, setProxy] = useState(initial)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        title={title}
        closeLabel={m.cancel}
        footer={
          <>
            <DialogClose asChild>
              <Button variant="secondary">{m.cancel}</Button>
            </DialogClose>
            <Button
              loading={pending}
              onClick={() => {
                onSave(proxy)
              }}
            >
              {m.save}
            </Button>
          </>
        }
      >
        <Field id="proxy" label={m.proxy} description={m.proxyHint}>
          <ProxyInput
            id="proxy"
            value={proxy}
            testLabel={m.testProxy}
            placeholder="http://127.0.0.1:7890"
            onChange={setProxy}
          />
        </Field>
      </DialogContent>
    </Dialog>
  )
}
