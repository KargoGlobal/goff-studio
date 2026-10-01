import { setShare, type Split } from '@/lib/split'

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
  return (
    <div className="space-y-2">
      {names.map((name) => (
        <div key={name} className="flex items-center gap-3">
          <span className="w-24 shrink-0 font-mono text-[12.5px] text-ink-soft">{name}</span>
          <input
            type="range"
            min={0}
            max={100}
            step={1}
            value={value[name] ?? 0}
            disabled={disabled}
            aria-label={`${labelPrefix}${name} percentage`}
            onChange={(e) => onChange(setShare(names, value, name, Number(e.target.value)))}
            className="flex-1 accent-[var(--color-brand)]"
          />
          <span className="w-12 text-right font-mono text-[12.5px]">{value[name] ?? 0}%</span>
        </div>
      ))}
    </div>
  )
}
