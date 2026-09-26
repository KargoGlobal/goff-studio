import { useState } from 'react'
import { Button, Input, Spinner } from '@/components/ui/primitives'
import { Dialog } from '@/components/ui/Dialog'
import { useCreateTeam } from '@/hooks/useFlags'
import { useToast } from '@/components/ui/Toast'

export function NewTeamDialog({
  env,
  open,
  onClose,
  onCreated,
}: {
  env: string
  open: boolean
  onClose: () => void
  onCreated: (name: string) => void
}) {
  const create = useCreateTeam(env)
  const toast = useToast()
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)

  function reset() {
    setName('')
    setError(null)
  }

  async function submit() {
    const trimmed = name.trim()
    if (trimmed === '') {
      setError('Give the team a name.')
      return
    }
    if (/[\s/\\]/.test(trimmed)) {
      setError('Use letters, numbers and dashes only.')
      return
    }

    try {
      await create.mutateAsync(trimmed)
      toast(`Created ${trimmed}.`)
      onCreated(trimmed)
      reset()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not create the team')
    }
  }

  return (
    <Dialog
      open={open}
      onClose={() => {
        reset()
        onClose()
      }}
      title="New team"
      footer={
        <>
          <Button
            variant="outline"
            onClick={() => {
              reset()
              onClose()
            }}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={create.isPending}>
            {create.isPending && <Spinner className="border-white/40 border-t-white" />}
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-ink-soft">
          Creates <code className="font-mono text-ink">{env}/{(name.trim() || '<team>')}.goff.yaml</code>.
          One file per team; owns who can edit flags on it.
        </p>

        <div>
          <label htmlFor="team-name" className="mb-1.5 block text-sm font-medium text-ink-soft">
            Name
          </label>
          <Input
            id="team-name"
            autoFocus
            value={name}
            onChange={(e) => {
              setName(e.target.value)
              setError(null)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void submit()
            }}
            placeholder="billing"
            className="h-11 font-mono text-base"
          />
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
