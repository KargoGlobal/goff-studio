import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from '@/lib/cn'

// A clean drawer-style single-select. Replaces the native <select> when we
// want control over chevron placement, option typography, and hover styling.
// Keyboard: Enter/Space to open, Escape/Tab to close, Up/Down to navigate.
export function Select<T extends string>({
  value,
  onChange,
  options,
  placeholder,
  id,
  className,
  ariaLabel,
  disabled,
}: {
  value: T | ''
  onChange: (v: T) => void
  options: { value: T; label: string; hint?: string }[]
  placeholder?: string
  id?: string
  className?: string
  ariaLabel?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [focusIdx, setFocusIdx] = useState<number>(() =>
    Math.max(0, options.findIndex((o) => o.value === value)),
  )
  const wrapperRef = useRef<HTMLDivElement>(null)

  const selected = options.find((o) => o.value === value)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!wrapperRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={wrapperRef} className={cn('relative', className)}>
      <button
        type="button"
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={ariaLabel}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
            setFocusIdx((i) => Math.min(options.length - 1, i + 1))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setOpen(true)
            setFocusIdx((i) => Math.max(0, i - 1))
          } else if ((e.key === 'Enter' || e.key === ' ') && open) {
            e.preventDefault()
            const picked = options[focusIdx]
            if (picked) {
              onChange(picked.value)
              setOpen(false)
            }
          }
        }}
        className={cn(
          'flex h-11 w-full items-center justify-between rounded-md border bg-surface pl-3 pr-4 font-mono text-base transition-colors focus:border-brand focus:outline-none',
          disabled && 'cursor-not-allowed opacity-50',
        )}
      >
        <span className={cn('truncate', !selected && 'text-ink-muted')}>
          {selected?.label ?? placeholder ?? '—'}
        </span>
        <ChevronDown
          className={cn(
            'ml-3 h-4 w-4 shrink-0 text-ink-muted transition-transform',
            open && 'rotate-180',
          )}
        />
      </button>

      {open && options.length > 0 && (
        <ul
          role="listbox"
          className="absolute left-0 right-0 top-[calc(100%+6px)] z-40 max-h-64 overflow-y-auto rounded-md border bg-surface py-1 shadow-lg"
        >
          {options.map((o, i) => {
            const isSelected = o.value === value
            const isFocused = i === focusIdx
            return (
              <li
                key={o.value}
                role="option"
                aria-selected={isSelected}
                onMouseEnter={() => setFocusIdx(i)}
                onClick={() => {
                  onChange(o.value)
                  setOpen(false)
                }}
                className={cn(
                  'flex cursor-pointer items-center justify-between gap-3 px-3 py-2 font-mono text-sm text-ink transition-colors',
                  isFocused && 'bg-[color:var(--color-row-hover)]',
                  isSelected && 'text-brand',
                )}
              >
                <span className="flex-1 truncate">
                  {o.label}
                  {o.hint && <span className="ml-2 text-[12px] text-ink-muted">{o.hint}</span>}
                </span>
                {isSelected && <Check className="h-4 w-4 shrink-0" />}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
