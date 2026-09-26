import { useMemo, useState } from 'react'
import { Plus, X } from 'lucide-react'
import type { Flag, NewVariation } from '@/lib/api'
import { Button } from '@/components/ui/primitives'
import { Select } from '@/components/ui/Select'

function toRaw(value: unknown, type: Flag['type']): string {
  if (value === null || value === undefined) return ''
  if (type === 'json') return JSON.stringify(value, null, 2)
  return String(value)
}

export function referencedBy(flag: Flag, name: string): string | null {
  if (flag.default?.variation === name) return 'the default'
  if (flag.default?.percentage && name in flag.default.percentage) return 'the default split'

  for (const rule of flag.rules ?? []) {
    if (rule.outcome?.variation === name) return `rule "${rule.name}"`
    if (rule.outcome?.percentage && name in rule.outcome.percentage) {
      return `rule "${rule.name}"'s split`
    }
  }
  return null
}

export function VariationsEditor({
  flag,
  onCancel,
  onSubmit,
}: {
  flag: Flag
  onCancel: () => void
  onSubmit: (variations: NewVariation[], defaultVariation: string) => void
}) {
  const [rows, setRows] = useState<NewVariation[]>(
    (flag.variations ?? []).map((v) => ({ name: v.name, value: toRaw(v.value, flag.type) })),
  )
  const [defaultName, setDefaultName] = useState(flag.default?.variation ?? '')
  const [error, setError] = useState<string | null>(null)

  const originalNames = useMemo(
    () => new Set((flag.variations ?? []).map((v) => v.name)),
    [flag.variations],
  )

  function update(index: number, patch: Partial<NewVariation>) {
    setRows((prev) => prev.map((row, i) => (i === index ? { ...row, ...patch } : row)))
    setError(null)
  }

  function remove(index: number) {
    const name = rows[index]?.name
    if (originalNames.has(name)) {
      const used = referencedBy(flag, name)
      if (used) {
        setError(`"${name}" cannot be removed while ${used} still serves it.`)
        return
      }
    }
    setRows((prev) => prev.filter((_, i) => i !== index))
    setError(null)
  }

  function submit() {
    const cleaned = rows
      .map((r) => ({ name: r.name.trim(), value: r.value }))
      .filter((r) => r.name !== '')

    if (cleaned.length === 0) {
      setError('A flag needs at least one variation.')
      return
    }

    const seen = new Set<string>()
    for (const row of cleaned) {
      if (seen.has(row.name)) {
        setError(`"${row.name}" is listed twice.`)
        return
      }
      seen.add(row.name)
    }

    if (flag.type === 'json') {
      for (const row of cleaned) {
        try {
          JSON.parse(row.value)
        } catch {
          setError(`"${row.name}" is not valid JSON.`)
          return
        }
      }
    }

    for (const original of originalNames) {
      if (!seen.has(original)) {
        const used = referencedBy(flag, original)
        if (used) {
          setError(`"${original}" cannot be removed while ${used} still serves it.`)
          return
        }
      }
    }

    if (!seen.has(defaultName)) {
      setError('Pick a default variation from the list above.')
      return
    }

    onSubmit(cleaned, defaultName)
  }

  return (
    <div className="space-y-3">
      <div className="space-y-2">
        {rows.map((row, i) => {
          const used = originalNames.has(row.name) ? referencedBy(flag, row.name) : null
          return (
            <div key={i} className="flex items-start gap-2">
              <input
                value={row.name}
                onChange={(e) => update(i, { name: e.target.value })}
                placeholder="name"
                aria-label={`Variation ${i + 1} name`}
                className="h-11 w-40 shrink-0 rounded-md border bg-surface px-2.5 font-mono text-sm focus:border-brand focus:outline-none"
              />

              {flag.type === 'boolean' ? (
                <div className="flex-1">
                  <Select
                    value={row.value}
                    onChange={(v) => update(i, { value: v })}
                    ariaLabel={`Variation ${i + 1} value`}
                    options={[
                      { value: 'true', label: 'true' },
                      { value: 'false', label: 'false' },
                    ]}
                  />
                </div>
              ) : flag.type === 'json' ? (
                <textarea
                  value={row.value}
                  onChange={(e) => update(i, { value: e.target.value })}
                  rows={2}
                  aria-label={`Variation ${i + 1} value`}
                  className="flex-1 rounded-md border bg-surface px-2.5 py-2 font-mono text-sm focus:border-brand focus:outline-none"
                />
              ) : (
                <input
                  type={flag.type === 'number' ? 'number' : 'text'}
                  value={row.value}
                  onChange={(e) => update(i, { value: e.target.value })}
                  placeholder="value"
                  aria-label={`Variation ${i + 1} value`}
                  className="h-11 flex-1 rounded-md border bg-surface px-2.5 font-mono text-sm focus:border-brand focus:outline-none"
                />
              )}

              <button
                type="button"
                onClick={() => remove(i)}
                title={used ? `Used by ${used}` : 'Remove'}
                aria-label={`Remove variation ${row.name || i + 1}`}
                className="h-11 w-11 shrink-0 rounded-md border text-ink-muted transition-colors hover:border-danger hover:text-danger disabled:opacity-40"
                disabled={Boolean(used)}
              >
                <X className="mx-auto h-4 w-4" />
              </button>
            </div>
          )
        })}
      </div>

      <Button
        size="sm"
        variant="outline"
        onClick={() => setRows((prev) => [...prev, { name: '', value: flag.type === 'boolean' ? 'false' : '' }])}
      >
        <Plus className="h-3.5 w-3.5" />
        Add variation
      </Button>

      <div>
        <label htmlFor="default-variation" className="mb-1.5 block text-sm font-medium text-ink-soft">
          Served when no rule matches
        </label>
        <Select
          id="default-variation"
          value={defaultName}
          onChange={(v) => setDefaultName(v)}
          ariaLabel="Default variation"
          placeholder="— pick one —"
          options={rows.filter((r) => r.name.trim() !== '').map((r) => ({ value: r.name, label: r.name }))}
          className="w-64"
        />
      </div>

      {error && (
        <p role="alert" className="rounded-md border border-danger bg-danger-soft px-3 py-2 text-[12.5px] text-ink">
          {error}
        </p>
      )}

      <div className="flex gap-2">
        <Button size="sm" aria-label="Review the variation changes" onClick={submit}>
          Review change
        </Button>
        <Button size="sm" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  )
}
