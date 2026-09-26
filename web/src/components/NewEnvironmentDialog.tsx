import { useState } from 'react'
import { Button, Input, Spinner } from '@/components/ui/primitives'
import { Dialog } from '@/components/ui/Dialog'
import { useCreateEnvironment } from '@/hooks/useFlags'
import { useToast } from '@/components/ui/Toast'

export function NewEnvironmentDialog({
  open,
  onClose,
}: {
  open: boolean
  onClose: () => void
}) {
  const create = useCreateEnvironment()
  const toast = useToast()
  const [name, setName] = useState('')
  const [file, setFile] = useState('flags')
  const [error, setError] = useState<string | null>(null)

  async function submit() {
    const trimmed = name.trim()
    if (trimmed === '') {
      setError('Give the environment a name.')
      return
    }
    if (/[\s/\\]/.test(trimmed)) {
      setError('Use letters, numbers and dashes only.')
      return
    }

    try {
      await create.mutateAsync({ name: trimmed, file: file.trim() || undefined })
      toast(`Created ${trimmed}.`)
      onClose()
      setName('')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not create the environment')
    }
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="New environment"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={create.isPending}>
            {create.isPending && <Spinner className="border-white/40 border-t-white" />}
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-ink-soft">
          An environment is a top-level directory of flag files. Studio will create it with one
          empty file so your apps can point at it.
        </p>

        <div>
          <label htmlFor="env-name" className="mb-1.5 block text-sm font-medium text-ink-soft">
            Name
          </label>
          <Input
            id="env-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value)
              setError(null)
            }}
            placeholder="staging"
            className="h-11 font-mono text-base"
          />
        </div>

        <div>
          <label htmlFor="env-file" className="mb-1.5 block text-sm font-medium text-ink-soft">
            First team
          </label>
          <Input
            id="env-file"
            value={file}
            onChange={(e) => setFile(e.target.value)}
            placeholder="flags"
            className="h-11 font-mono text-base"
          />
          <p className="mt-1.5 text-[13px] text-ink-muted">
            One file per team. Creates{' '}
            <code className="font-mono text-ink">
              {(name.trim() || 'staging')}/{(file.trim() || 'flags').replace(/\.(goff\.)?ya?ml$/, '')}
              .goff.yaml
            </code>
            .
          </p>
        </div>

        {error && (
          <p role="alert" className="rounded-md border border-danger bg-danger-soft px-3 py-2 text-sm text-ink">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  )
}
