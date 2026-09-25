import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Plus, X } from 'lucide-react'
import { ApiError, type Environment, type NewVariation } from '@/lib/api'
import { useCreateFlag, useCreateTeam, useFlags, useMe } from '@/hooks/useFlags'
import { Button, Card, Input, Spinner } from '@/components/ui/primitives'
import { useToast } from '@/components/ui/Toast'

const TYPES = [
  { value: 'boolean', label: 'On / off', hint: 'true or false' },
  { value: 'string', label: 'Text', hint: 'a string value' },
  { value: 'number', label: 'Number', hint: 'a numeric value' },
  { value: 'json', label: 'JSON', hint: 'a structured object' },
] as const

const DEFAULTS: Record<string, NewVariation[]> = {
  boolean: [
    { name: 'on', value: 'true' },
    { name: 'off', value: 'false' },
  ],
  string: [
    { name: 'control', value: '' },
    { name: 'treatment', value: '' },
  ],
  number: [
    { name: 'control', value: '0' },
    { name: 'treatment', value: '1' },
  ],
  json: [{ name: 'config', value: '{}' }],
}

export function CreateFlagPage({ environments }: { environments: Environment[] }) {
  const { env = '' } = useParams()
  const navigate = useNavigate()
  const environment = environments.find((e) => e.name === env)
  const { data } = useFlags(env)
  const { data: me } = useMe()
  const create = useCreateFlag(env)
  const createTeam = useCreateTeam(env)
  const toast = useToast()

  const [key, setKey] = useState('')
  const [team, setTeam] = useState('')
  const [newTeam, setNewTeam] = useState<string | null>(null)
  const [type, setType] = useState<string>('boolean')
  const [variations, setVariations] = useState<NewVariation[]>(DEFAULTS.boolean)
  const [defaultName, setDefaultName] = useState('off')
  const [enabled, setEnabled] = useState(true)
  const [fieldError, setFieldError] = useState<{ field: string; message: string } | null>(null)

  const teams = data?.teams ?? []
  const chosenTeam = team || teams[0]?.name || ''
  const chosenFile = teams.find((t) => t.name === chosenTeam)?.file ?? ''

  async function addTeam() {
    const name = (newTeam ?? '').trim()
    if (name === '') {
      setFieldError({ field: 'newTeam', message: 'Give the team a name.' })
      return
    }
    if (/[\s/\\]/.test(name)) {
      setFieldError({ field: 'newTeam', message: 'Use letters, numbers and dashes only.' })
      return
    }
    try {
      await createTeam.mutateAsync(name)
      setTeam(name)
      setNewTeam(null)
      setFieldError(null)
      toast(`Created ${name}.`)
    } catch (e) {
      setFieldError({
        field: 'newTeam',
        message: e instanceof Error ? e.message : 'Could not create the team',
      })
    }
  }

  function changeType(next: string) {
    setType(next)
    const seeded = DEFAULTS[next] ?? DEFAULTS.string
    setVariations(seeded)
    setDefaultName(seeded.at(-1)?.name ?? '')
    setFieldError(null)
  }

  function validate(): string | null {
    if (key.trim() === '') return 'Give the flag a key.'
    if (/\s/.test(key.trim())) return 'A flag key cannot contain spaces.'
    if (!chosenTeam) return 'Pick a team for this flag.'

    const named = variations.filter((v) => v.name.trim() !== '')
    if (named.length === 0) return 'Add at least one variation.'

    const seen = new Set<string>()
    for (const v of named) {
      if (seen.has(v.name.trim())) return `"${v.name.trim()}" is listed twice.`
      seen.add(v.name.trim())
    }

    if (type === 'json') {
      for (const v of named) {
        try {
          JSON.parse(v.value)
        } catch {
          return `"${v.name}" is not valid JSON.`
        }
      }
    }
    if (type === 'number') {
      for (const v of named) {
        if (v.value.trim() === '' || Number.isNaN(Number(v.value))) {
          return `"${v.name}" needs a number.`
        }
      }
    }

    if (!seen.has(defaultName)) return 'Pick which variation is served by default.'
    return null
  }

  async function submit() {
    const problem = validate()
    if (problem) {
      setFieldError({ field: 'form', message: problem })
      return
    }
    setFieldError(null)

    try {
      const result = await create.mutateAsync({
        key: key.trim(),
        team: chosenTeam,
        type,
        variations: variations.filter((v) => v.name.trim() !== ''),
        default: defaultName,
        enabled,
      })
      toast(result.message)
      navigate(`/env/${env}/flags/${encodeURIComponent(key.trim())}`)
    } catch (e) {
      if (e instanceof ApiError && e.isConflict) {
        setFieldError({ field: 'key', message: e.message })
        return
      }
      setFieldError({ field: 'form', message: e instanceof Error ? e.message : 'Could not create the flag' })
    }
  }

  return (
    <div className="mx-auto max-w-2xl space-y-5">
      <Link
        to={`/env/${env}`}
        className="inline-flex items-center gap-2 text-base font-medium text-ink-muted hover:text-ink"
      >
        <ArrowLeft className="h-5 w-5" />
        All flags
      </Link>

      <div>
        <h1 className="text-xl font-semibold tracking-tight">Create a flag</h1>
        <p className="mt-0.5 text-[13px] text-ink-muted">
          In {environment?.display ?? env}. It starts off serving the default to everyone; add
          targeting afterwards.
        </p>
      </div>

      <Card className="space-y-4 p-4">
        <div>
          <label htmlFor="flag-key" className="mb-1 block text-[12px] text-ink-soft">
            Flag key
          </label>
          <Input
            id="flag-key"
            value={key}
            onChange={(e) => {
              setKey(e.target.value)
              setFieldError(null)
            }}
            placeholder="new-checkout"
            className="font-mono"
            aria-invalid={fieldError?.field === 'key'}
          />
          <p className="mt-1 text-[11.5px] text-ink-muted">
            This is what your code asks for. It cannot be changed later.
          </p>
          {fieldError?.field === 'key' && (
            <p role="alert" className="mt-1.5 text-[12.5px] text-danger">
              {fieldError.message}
            </p>
          )}
        </div>

        <div>
          <div className="mb-1 flex items-baseline justify-between gap-3">
            <label htmlFor="flag-team" className="text-[12px] text-ink-soft">
              Team
            </label>
            {newTeam === null && (
              <button
                type="button"
                onClick={() => setNewTeam('')}
                className="text-[12px] text-brand hover:underline"
              >
                New team
              </button>
            )}
          </div>
          {newTeam === null ? (
            <select
              id="flag-team"
              value={chosenTeam}
              onChange={(e) => setTeam(e.target.value)}
              className="h-9 w-full rounded-md border bg-surface px-2.5 font-mono text-[13px] focus:border-brand focus:outline-none"
            >
              {teams.length === 0 && <option value="">No teams yet</option>}
              {teams.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                </option>
              ))}
            </select>
          ) : (
            <div className="flex gap-2">
              <Input
                autoFocus
                value={newTeam}
                onChange={(e) => {
                  setNewTeam(e.target.value)
                  setFieldError(null)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void addTeam()
                  if (e.key === 'Escape') setNewTeam(null)
                }}
                placeholder="billing"
                className="font-mono"
                aria-label="New team name"
              />
              <Button onClick={() => void addTeam()} disabled={createTeam.isPending}>
                {createTeam.isPending && <Spinner className="border-white/40 border-t-white" />}
                Add
              </Button>
              <Button variant="outline" onClick={() => setNewTeam(null)}>
                Cancel
              </Button>
            </div>
          )}
          <p className="mt-1 text-[11.5px] text-ink-muted">
            Written to <span className="font-mono">metadata.team</span> and stored in{' '}
            <span className="font-mono">{chosenFile || `${env}/<team>.goff.yaml`}</span>, which
            decides who may edit it
            {me?.capabilities?.review !== false && ' and who reviews changes via CODEOWNERS'}.
          </p>
          {fieldError?.field === 'newTeam' && (
            <p role="alert" className="mt-1.5 text-[12.5px] text-danger">
              {fieldError.message}
            </p>
          )}
        </div>

        <div>
          <span className="mb-1.5 block text-[12px] text-ink-soft">What does it return?</span>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {TYPES.map((t) => (
              <button
                key={t.value}
                type="button"
                onClick={() => changeType(t.value)}
                aria-pressed={type === t.value}
                className={
                  type === t.value
                    ? 'rounded-lg border border-brand bg-brand-soft px-3 py-2 text-left'
                    : 'rounded-lg border px-3 py-2 text-left hover:bg-canvas'
                }
              >
                <span className="block text-[13px] font-medium">{t.label}</span>
                <span className="block text-[11px] text-ink-muted">{t.hint}</span>
              </button>
            ))}
          </div>
        </div>
      </Card>

      <Card className="space-y-3 p-4">
        <div>
          <h2 className="text-[13px] font-semibold uppercase tracking-wide text-ink-muted">
            Variations
          </h2>
          <p className="mt-0.5 text-[11.5px] text-ink-muted">The possible values this flag returns.</p>
        </div>

        <div className="space-y-2">
          {variations.map((row, i) => (
            <div key={i} className="flex items-start gap-2">
              <input
                value={row.name}
                onChange={(e) =>
                  setVariations((prev) =>
                    prev.map((v, j) => (j === i ? { ...v, name: e.target.value } : v)),
                  )
                }
                placeholder="name"
                aria-label={`Variation ${i + 1} name`}
                className="h-8 w-32 shrink-0 rounded-md border bg-surface px-2 font-mono text-[12.5px] focus:border-brand focus:outline-none"
              />

              {type === 'boolean' ? (
                <select
                  value={row.value}
                  onChange={(e) =>
                    setVariations((prev) =>
                      prev.map((v, j) => (j === i ? { ...v, value: e.target.value } : v)),
                    )
                  }
                  aria-label={`Variation ${i + 1} value`}
                  className="h-8 flex-1 rounded-md border bg-surface px-2 font-mono text-[12.5px] focus:border-brand focus:outline-none"
                >
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              ) : type === 'json' ? (
                <textarea
                  value={row.value}
                  onChange={(e) =>
                    setVariations((prev) =>
                      prev.map((v, j) => (j === i ? { ...v, value: e.target.value } : v)),
                    )
                  }
                  rows={2}
                  aria-label={`Variation ${i + 1} value`}
                  className="flex-1 rounded-md border bg-surface px-2 py-1.5 font-mono text-[12.5px] focus:border-brand focus:outline-none"
                />
              ) : (
                <input
                  type={type === 'number' ? 'number' : 'text'}
                  value={row.value}
                  onChange={(e) =>
                    setVariations((prev) =>
                      prev.map((v, j) => (j === i ? { ...v, value: e.target.value } : v)),
                    )
                  }
                  placeholder="value"
                  aria-label={`Variation ${i + 1} value`}
                  className="h-8 flex-1 rounded-md border bg-surface px-2 font-mono text-[12.5px] focus:border-brand focus:outline-none"
                />
              )}

              <button
                type="button"
                onClick={() => setVariations((prev) => prev.filter((_, j) => j !== i))}
                aria-label={`Remove variation ${row.name || i + 1}`}
                disabled={variations.length === 1}
                className="h-8 w-8 shrink-0 rounded-md border text-ink-muted hover:border-danger hover:text-danger disabled:opacity-40"
              >
                <X className="mx-auto h-3.5 w-3.5" />
              </button>
            </div>
          ))}
        </div>

        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            setVariations((prev) => [...prev, { name: '', value: type === 'boolean' ? 'false' : '' }])
          }
        >
          <Plus className="h-3.5 w-3.5" />
          Add variation
        </Button>

        <div>
          <label htmlFor="flag-default" className="mb-1 block text-[12px] text-ink-soft">
            Served when no rule matches
          </label>
          <select
            id="flag-default"
            value={defaultName}
            onChange={(e) => setDefaultName(e.target.value)}
            className="h-8 w-48 rounded-md border bg-surface px-2 font-mono text-[12.5px] focus:border-brand focus:outline-none"
          >
            <option value="">— pick one —</option>
            {variations
              .filter((v) => v.name.trim() !== '')
              .map((v) => (
                <option key={v.name} value={v.name}>
                  {v.name}
                </option>
              ))}
          </select>
        </div>

        <label className="flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            className="accent-[var(--color-brand)]"
          />
          Turn this flag on straight away
        </label>
      </Card>

      {fieldError?.field === 'form' && (
        <p
          role="alert"
          className="rounded-lg border border-danger bg-danger-soft px-3 py-2.5 text-[13px] text-ink"
        >
          {fieldError.message}
        </p>
      )}

      <div className="flex gap-2">
        <Button onClick={() => void submit()} disabled={create.isPending}>
          {create.isPending && <Spinner className="border-white/40 border-t-white" />}
          Create flag
        </Button>
        <Button variant="outline" onClick={() => navigate(`/env/${env}`)}>
          Cancel
        </Button>
      </div>
    </div>
  )
}
