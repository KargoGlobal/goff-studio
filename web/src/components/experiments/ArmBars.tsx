import { variantColor } from '@/components/experiments/series'

interface Arm {
  key: string
  is_control: boolean
  units: number
  expected_share: number
}

export function ArmBars({ arms, variants, control }: { arms: Arm[]; variants: string[]; control: string }) {
  const total = arms.reduce((sum, a) => sum + a.units, 0)
  if (total === 0) return null

  return (
    <ul className="space-y-2.5" aria-label="Units per variant">
      {arms.map((a) => {
        const share = a.units / total
        const tip = `${a.key}: ${a.units.toLocaleString('en-US')} units, ${(share * 100).toFixed(2)}% of traffic (planned ${(a.expected_share * 100).toFixed(1)}%)`
        return (
          <li key={a.key} title={tip} className="grid grid-cols-[7rem_1fr_7rem] items-center gap-3 text-[12.5px]">
            <span className="truncate font-mono text-ink">
              {a.key}
              {a.is_control && <span className="ml-1 text-ink-muted">(control)</span>}
            </span>
            <svg viewBox="0 0 100 10" preserveAspectRatio="none" className="h-2.5 w-full" role="img" aria-label={tip}>
              <rect x={0} y={0} width={100} height={10} rx={2} fill="var(--color-canvas)" />
              <rect x={0} y={0} width={Math.max(share * 100, 1)} height={10} rx={2} fill={variantColor(variants, control, a.key)} />
              <line
                x1={a.expected_share * 100}
                x2={a.expected_share * 100}
                y1={-2}
                y2={12}
                stroke="var(--color-ink)"
                strokeWidth={0.6}
                vectorEffect="non-scaling-stroke"
              />
            </svg>
            <span className="text-right tabular-nums text-ink-soft">
              {a.units.toLocaleString('en-US')} · {(share * 100).toFixed(1)}%
            </span>
          </li>
        )
      })}
    </ul>
  )
}
