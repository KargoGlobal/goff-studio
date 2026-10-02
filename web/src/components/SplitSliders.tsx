import { useState } from 'react'
import { setShare, type Split } from '@/lib/split'

// Must match the thumb size set via the [&::-webkit-slider-thumb] / [&::-moz-range-thumb]
// classes below, so the tooltip tracks the thumb's actual center, not the raw percentage.
const THUMB_PX = 16

export function SplitSliders({
  names,
  value,
  onChange,
  disabled = false,
  labelPrefix = '',
}: {
  names: string[]
  value: Split
  onChange: (next: Split) => void
  disabled?: boolean
  labelPrefix?: string
}) {
  const [active, setActive] = useState<string | null>(null)

  return (
    <div className="space-y-2">
      {names.map((name) => {
        const pct = value[name] ?? 0
        return (
          <div key={name} className="flex items-center gap-3">
            <span className="w-24 shrink-0 font-mono text-[12.5px] text-ink-soft max-md:w-20 max-md:truncate">{name}</span>
            <div className="flex flex-1 items-center gap-3">
              <div className="relative flex-1 max-md:min-w-0">
                {active === name && (
                  <div
                    style={{ left: `calc(${THUMB_PX / 2}px + (100% - ${THUMB_PX}px) * ${pct / 100})` }}
                    className="pointer-events-none absolute bottom-full mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-md bg-[#1c1c1d] px-2 py-1 font-mono text-[11px] font-semibold text-white dark:bg-white dark:text-[#1c1c1d]"
                  >
                    {pct}%
                  </div>
                )}
                <input
                  type="range"
                  min={0}
                  max={100}
                  step={1}
                  value={pct}
                  disabled={disabled}
                  aria-label={`${labelPrefix}${name} percentage`}
                  onChange={(e) => onChange(setShare(names, value, name, Number(e.target.value)))}
                  onMouseEnter={() => setActive(name)}
                  onMouseLeave={() => setActive((a) => (a === name ? null : a))}
                  onPointerDown={() => setActive(name)}
                  onPointerUp={() => setActive(null)}
                  onPointerCancel={() => setActive(null)}
                  onFocus={() => setActive(name)}
                  onBlur={() => setActive((a) => (a === name ? null : a))}
                  className="w-full accent-[var(--color-brand)] max-md:h-11 [&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:w-4 [&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:w-4"
                />
              </div>
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border-[3px] border-ink bg-canvas font-mono text-[11px] font-extrabold leading-none tracking-tight text-ink dark:border-white">
                {pct}%
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
