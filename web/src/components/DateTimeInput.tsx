import { useEffect, useMemo, useRef, useState } from 'react'
import { DayPicker } from 'react-day-picker'
import 'react-day-picker/style.css'
import { Calendar as CalendarIcon, Clock } from 'lucide-react'
import { toISO } from '@/lib/dates'
import { cn } from '@/lib/cn'

function fmtDisplay(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  const day = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${day} · ${time}`
}

function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

export function DateTimeInput({
  label,
  value,
  onChange,
  className,
}: {
  label: string
  value: string
  onChange: (iso: string) => void
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const wrapperRef = useRef<HTMLDivElement>(null)

  const current = useMemo(() => {
    if (!value) return null
    const d = new Date(value)
    return Number.isNaN(d.getTime()) ? null : d
  }, [value])

  const [hours, setHours] = useState<string>(current ? pad2(current.getHours()) : '09')
  const [minutes, setMinutes] = useState<string>(current ? pad2(current.getMinutes()) : '00')

  useEffect(() => {
    if (current) {
      setHours(pad2(current.getHours()))
      setMinutes(pad2(current.getMinutes()))
    }
  }, [current])

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

  function commit(day: Date | undefined, h: string, m: string) {
    const base = day ?? current ?? new Date()
    const hh = Math.max(0, Math.min(23, Number(h) || 0))
    const mm = Math.max(0, Math.min(59, Number(m) || 0))
    const next = new Date(
      base.getFullYear(),
      base.getMonth(),
      base.getDate(),
      hh,
      mm,
    )
    // toISO expects a local `YYYY-MM-DDTHH:MM` string.
    const local = `${next.getFullYear()}-${pad2(next.getMonth() + 1)}-${pad2(next.getDate())}T${pad2(hh)}:${pad2(mm)}`
    onChange(toISO(local))
  }

  const displayed = current ? fmtDisplay(value) : 'YYYY-MM-DD · HH:MM'

  return (
    <div ref={wrapperRef} className={cn('relative w-64', className)}>
      <button
        type="button"
        aria-label={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'inline-flex h-11 w-full items-center gap-2 rounded-md border bg-surface pl-3 pr-3 font-mono text-sm text-ink transition-colors focus:border-brand focus:outline-none',
          !current && 'text-ink-muted',
        )}
      >
        <CalendarIcon className="h-4 w-4 shrink-0 text-brand" aria-hidden />
        <span className="flex-1 truncate text-left">{displayed}</span>
      </button>

      {open && (
        <div
          role="dialog"
          aria-label={`${label} picker`}
          className="absolute left-0 top-[calc(100%+6px)] z-40 rounded-lg border bg-surface p-3 shadow-lg"
        >
          <DayPicker
            mode="single"
            selected={current ?? undefined}
            onSelect={(day) => {
              if (!day) return
              commit(day, hours, minutes)
            }}
            weekStartsOn={1}
            showOutsideDays
            classNames={{
              months: 'flex',
              month: 'space-y-2',
              month_caption: 'flex items-center justify-center py-1',
              caption_label: 'text-sm font-semibold text-ink',
              nav: 'flex items-center justify-between px-1 pb-1',
              button_previous:
                'h-7 w-7 rounded-md text-brand transition-colors hover:bg-[color:var(--color-brand-soft)] hover:text-brand-strong',
              button_next:
                'h-7 w-7 rounded-md text-brand transition-colors hover:bg-[color:var(--color-brand-soft)] hover:text-brand-strong',
              chevron: 'fill-current',
              month_grid: 'border-collapse',
              weekdays: 'flex',
              weekday:
                'w-9 pb-1 text-[11px] font-semibold uppercase tracking-wide text-ink-muted',
              week: 'flex',
              day: 'p-0.5',
              day_button:
                'h-9 w-9 rounded-md text-sm text-ink transition-colors hover:bg-[color:var(--color-row-hover)] focus:bg-[color:var(--color-row-hover)] focus:outline-none',
              selected:
                '[&_.rdp-day_button]:bg-brand [&_.rdp-day_button]:text-white [&_.rdp-day_button]:hover:bg-brand',
              today: '[&_.rdp-day_button]:font-bold [&_.rdp-day_button]:text-brand',
              outside: '[&_.rdp-day_button]:text-ink-muted [&_.rdp-day_button]:opacity-40',
              disabled: '[&_.rdp-day_button]:text-ink-muted [&_.rdp-day_button]:opacity-30',
            }}
          />

          <div className="mt-2 flex items-center justify-between gap-2 border-t pt-2.5">
            <div className="flex items-center gap-1.5 text-ink-muted">
              <Clock className="h-4 w-4 text-brand" aria-hidden />
              <span className="text-[13px]">Time</span>
            </div>
            <div className="inline-flex h-9 items-center gap-0.5 rounded-md border bg-surface px-2 focus-within:border-brand">
              <input
                type="number"
                min={0}
                max={23}
                value={hours}
                onChange={(e) => {
                  setHours(e.target.value)
                }}
                onBlur={() => {
                  const v = pad2(Math.max(0, Math.min(23, Number(hours) || 0)))
                  setHours(v)
                  commit(current ?? undefined, v, minutes)
                }}
                aria-label="Hours"
                className="w-9 bg-transparent text-center font-mono text-sm text-ink focus:outline-none"
              />
              <span className="text-ink-muted">:</span>
              <input
                type="number"
                min={0}
                max={59}
                value={minutes}
                onChange={(e) => setMinutes(e.target.value)}
                onBlur={() => {
                  const v = pad2(Math.max(0, Math.min(59, Number(minutes) || 0)))
                  setMinutes(v)
                  commit(current ?? undefined, hours, v)
                }}
                aria-label="Minutes"
                className="w-9 bg-transparent text-center font-mono text-sm text-ink focus:outline-none"
              />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
