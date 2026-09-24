import { useState, type KeyboardEvent, type ClipboardEvent } from 'react'
import { X } from 'lucide-react'

function split(raw: string): string[] {
  return raw
    .split(',')
    .map((v) => v.trim())
    .filter((v) => v !== '')
}

export function ChipInput({
  values,
  onChange,
  placeholder,
  label,
  disabled,
}: {
  values: string[]
  onChange: (next: string[]) => void
  placeholder?: string
  label: string
  disabled?: boolean
}) {
  const [draft, setDraft] = useState('')

  function add(raw: string) {
    const incoming = split(raw).filter((v) => !values.includes(v))
    if (incoming.length > 0) onChange([...values, ...incoming])
    setDraft('')
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',' || e.key === 'Tab') {
      if (draft.trim() !== '') {
        e.preventDefault()
        add(draft)
      }
      return
    }
    if (e.key === 'Backspace' && draft === '' && values.length > 0) {
      e.preventDefault()
      onChange(values.slice(0, -1))
    }
  }

  function onPaste(e: ClipboardEvent<HTMLInputElement>) {
    const text = e.clipboardData.getData('text')
    if (text.includes(',') || text.includes('\n')) {
      e.preventDefault()
      add(text.replace(/\n/g, ','))
    }
  }

  return (
    <div
      className="flex min-h-8 min-w-48 flex-1 flex-wrap items-center gap-1.5 rounded-md border bg-surface px-1.5 py-1 focus-within:border-brand"
      role="group"
      aria-label={label}
    >
      {values.map((value) => (
        <span
          key={value}
          className="inline-flex items-center gap-1 rounded bg-brand-soft px-1.5 py-0.5 font-mono text-[12px] text-brand"
        >
          {value}
          {!disabled && (
            <button
              type="button"
              aria-label={`Remove ${value}`}
              onClick={() => onChange(values.filter((v) => v !== value))}
              className="text-brand/60 hover:text-brand"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </span>
      ))}

      <input
        type="text"
        value={draft}
        disabled={disabled}
        aria-label={values.length > 0 ? `${label}, add another` : label}
        placeholder={values.length === 0 ? placeholder : ''}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        onBlur={() => draft.trim() !== '' && add(draft)}
        className="min-w-24 flex-1 bg-transparent px-1 font-mono text-[12.5px] text-ink placeholder:text-ink-muted focus:outline-none"
      />
    </div>
  )
}
