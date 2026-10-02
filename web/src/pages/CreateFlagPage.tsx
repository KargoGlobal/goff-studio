import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Plus, X } from 'lucide-react'
import { ApiError, type Environment, type NewVariation } from '@/lib/api'
import { useCreateFlag, useFlags, useMe } from '@/hooks/useFlags'
import { Button, Card, Input, Spinner } from '@/components/ui/primitives'
import { Select } from '@/components/ui/Select'
import { NewTeamDialog } from '@/components/NewTeamDialog'
import { useToast } from '@/components/ui/Toast'
import { identifierInputProps } from '@/lib/inputProps'
import { MAX_DESCRIPTION } from '@/lib/description'

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
  const toast = useToast()

  const [key, setKey] = useState('')
  const [team, setTeam] = useState('')
  const [teamDialogOpen, setTeamDialogOpen] = useState(false)
  const [type, setType] = useState<string>('boolean')
  const [variations, setVariations] = useState<NewVariation[]>(DEFAULTS.boolean)
  const [defaultName, setDefaultName] = useState('off')
  const [enabled, setEnabled] = useState(true)
  const [description, setDescription] = useState('')
  const [fieldError, setFieldError] = useState<{ field: string; message: string } | null>(null)

  const singleFile = me?.layout === 'single-file'
  const [draftTeam, setDraftTeam] = useState('')
  const listed = data?.teams ?? []
  const teams =
    draftTeam && !listed.some((t) => t.name === draftTeam)
      ? [...listed, { name: draftTeam, file: `${env}/flags.goff.yaml` }]
      : listed
  const chosenTeam = team || teams[0]?.name || ''
  const chosenFile = teams.find((t) => t.name === chosenTeam)?.file ?? ''

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
        description: description.trim(),
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
    <div className="mx-auto max-w-4xl space-y-6">
      <Link
        to={`/env/${env}`}
        className="inline-flex items-center gap-2 text-lg font-semibold text-ink-muted transition-colors hover:text-ink max-md:min-h-11 max-md:max-w-full"
      >
        <ArrowLeft className="h-6 w-6 max-md:shrink-0" />
        All flags
      </Link>

      <div>
        <h1 className="text-4xl font-bold tracking-tight max-md:text-3xl">Create a flag</h1>
        <p className="mt-1 text-base text-ink-muted">
          In {environment?.name ?? env}. It starts off serving the default to everyone; add
          targeting afterwards.
        </p>
      </div>

      <Card className="space-y-6 p-6 max-md:p-4">
        <div>
          <label htmlFor="flag-key" className="mb-1.5 block text-sm font-medium text-ink-soft">
            Flag key
          </label>
          <Input
            {...identifierInputProps}
            id="flag-key"
            value={key}
            onChange={(e) => {
              setKey(e.target.value)
              setFieldError(null)
            }}
            placeholder="new-checkout"
            className="h-11 font-mono text-base"
            aria-invalid={fieldError?.field === 'key'}
          />
          <p className="mt-1.5 text-[13px] text-ink-muted">
            This is what your code asks for. It cannot be changed later.
          </p>
          {fieldError?.field === 'key' && (
            <p role="alert" className="mt-2 text-sm text-danger">
              {fieldError.message}
            </p>
          )}
        </div>

        <div>
          <label htmlFor="flag-description" className="mb-1.5 block text-sm font-medium text-ink-soft">
            Description <span className="font-normal text-ink-muted">(optional)</span>
          </label>
          <Input
            id="flag-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={MAX_DESCRIPTION}
            placeholder="What this flag controls and why it exists"
            className="h-11 text-base"
          />
        </div>

        <div>
          <div className="mb-1.5 flex items-baseline justify-between gap-3">
            <label htmlFor="flag-team" className="text-sm font-medium text-ink-soft">
              Team
            </label>
            <button
              type="button"
              onClick={() => setTeamDialogOpen(true)}
              className="text-sm font-medium text-brand hover:underline max-md:-my-3 max-md:min-h-11"
            >
              New team
            </button>
          </div>
          <Select
            id="flag-team"
            value={chosenTeam}
            onChange={(v) => setTeam(v)}
            options={teams.map((t) => ({
              value: t.name,
              label: t.name === draftTeam && !listed.some((l) => l.name === draftTeam) ? `${t.name} (new)` : t.name,
            }))}
            placeholder={teams.length === 0 ? 'No teams yet' : 'Pick a team'}
            ariaLabel="Team"
          />
          {singleFile ? (
            <p className="mt-1.5 text-[13px] text-ink-muted">
              Written to <span className="font-mono">metadata.team</span>, which decides who may edit it.
              {chosenTeam === draftTeam && !listed.some((l) => l.name === draftTeam) &&
                ` ${draftTeam} is new and is only saved once you create this flag.`}
            </p>
          ) : (
            <p className="mt-1.5 text-[13px] text-ink-muted">
              Written to <span className="font-mono">metadata.team</span> and stored in{' '}
              <span className="font-mono max-md:wrap-anywhere">{chosenFile || `${env}/<team>.goff.yaml`}</span>, which
              decides who may edit it
              {me?.capabilities?.review !== false && ' and who reviews changes via CODEOWNERS'}.
            </p>
          )}
        </div>

        <div>
          <span className="mb-2 block text-sm font-medium text-ink-soft">What does it return?</span>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {TYPES.map((t) => (
              <button
                key={t.value}
                type="button"
                onClick={() => changeType(t.value)}
                aria-pressed={type === t.value}
                className={
                  type === t.value
                    ? 'rounded-lg border-2 border-brand bg-brand-soft px-4 py-3 text-left'
                    : 'rounded-lg border px-4 py-3 text-left transition-colors hover:bg-canvas'
                }
              >
                <span className="block text-base font-semibold">{t.label}</span>
                <span className="mt-0.5 block text-[13px] text-ink-muted">{t.hint}</span>
              </button>
            ))}
          </div>
        </div>
      </Card>

      <Card className="space-y-4 p-6 max-md:p-4">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">Variations</h2>
          <p className="mt-1 text-[13px] text-ink-muted">The possible values this flag returns.</p>
        </div>

        <div className="space-y-2">
          {variations.map((row, i) => (
            <div key={i} className="flex items-start gap-2 max-md:flex-wrap">
              <input
                value={row.name}
                onChange={(e) =>
                  setVariations((prev) =>
                    prev.map((v, j) => (j === i ? { ...v, name: e.target.value } : v)),
                  )
                }
                placeholder="name"
                aria-label={`Variation ${i + 1} name`}
                {...identifierInputProps}
                className="h-10 w-40 shrink-0 rounded-md border bg-surface px-2.5 font-mono text-sm focus:border-brand focus:outline-none max-md:h-11 max-md:w-auto max-md:min-w-0 max-md:flex-1"
              />

              {type === 'boolean' ? (
                <div className="flex-1 max-md:order-last max-md:basis-full">
                  <Select
                    value={row.value}
                    onChange={(v) =>
                      setVariations((prev) =>
                        prev.map((v2, j) => (j === i ? { ...v2, value: v } : v2)),
                      )
                    }
                    options={[
                      { value: 'true', label: 'true' },
                      { value: 'false', label: 'false' },
                    ]}
                    ariaLabel={`Variation ${i + 1} value`}
                  />
                </div>
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
                  {...identifierInputProps}
                  className="flex-1 rounded-md border bg-surface px-2.5 py-2 font-mono text-sm focus:border-brand focus:outline-none max-md:order-last max-md:basis-full"
                />
              ) : (
                <input
                  type={type === 'number' ? 'number' : 'text'}
                  inputMode={type === 'number' ? 'decimal' : undefined}
                  value={row.value}
                  onChange={(e) =>
                    setVariations((prev) =>
                      prev.map((v, j) => (j === i ? { ...v, value: e.target.value } : v)),
                    )
                  }
                  placeholder="value"
                  aria-label={`Variation ${i + 1} value`}
                  {...identifierInputProps}
                  className="h-10 flex-1 rounded-md border bg-surface px-2.5 font-mono text-sm focus:border-brand focus:outline-none max-md:order-last max-md:h-11 max-md:basis-full"
                />
              )}

              <button
                type="button"
                onClick={() => setVariations((prev) => prev.filter((_, j) => j !== i))}
                aria-label={`Remove variation ${row.name || i + 1}`}
                disabled={variations.length === 1}
                className="h-10 w-10 shrink-0 rounded-md border text-ink-muted max-md:h-11 max-md:w-11 transition-colors hover:border-danger hover:text-danger disabled:opacity-40"
              >
                <X className="mx-auto h-4 w-4" />
              </button>
            </div>
          ))}
        </div>

        <Button
          variant="outline"
          onClick={() =>
            setVariations((prev) => [...prev, { name: '', value: type === 'boolean' ? 'false' : '' }])
          }
        >
          <Plus className="h-4 w-4" />
          Add variation
        </Button>

        <div>
          <label htmlFor="flag-default" className="mb-1.5 block text-sm font-medium text-ink-soft">
            Served when no rule matches
          </label>
          <Select
            id="flag-default"
            value={defaultName}
            onChange={(v) => setDefaultName(v)}
            options={variations
              .filter((v) => v.name.trim() !== '')
              .map((v) => ({ value: v.name, label: v.name }))}
            placeholder="— pick one —"
            ariaLabel="Default variation"
            className="w-64 max-md:w-full"
          />
        </div>

        <label className="flex items-center gap-2.5 text-base max-md:min-h-11">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            className="h-4 w-4 accent-[var(--color-brand)]"
          />
          Turn this flag on straight away
        </label>
      </Card>

      {fieldError?.field === 'form' && (
        <p
          role="alert"
          className="rounded-lg border border-danger bg-danger-soft px-4 py-3 text-sm text-ink"
        >
          {fieldError.message}
        </p>
      )}

      <div className="flex gap-3 max-md:flex-col-reverse max-md:pb-2">
        <Button onClick={() => void submit()} disabled={create.isPending}>
          {create.isPending && <Spinner className="border-white/40 border-t-white" />}
          Create flag
        </Button>
        <Button variant="outline" onClick={() => navigate(`/env/${env}`)}>
          Cancel
        </Button>
      </div>

      <NewTeamDialog
        env={env}
        open={teamDialogOpen}
        onClose={() => setTeamDialogOpen(false)}
        onCreated={(name) => {
          if (singleFile) setDraftTeam(name)
          setTeam(name)
        }}
        singleFile={singleFile}
      />
    </div>
  )
}
