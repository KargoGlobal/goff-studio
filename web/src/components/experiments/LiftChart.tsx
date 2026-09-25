import { useMemo, useState } from 'react'
import { formatCI, formatLift, type SeriesPoint } from '@/lib/experiments'
import { variantColor } from '@/components/experiments/series'

const W = 640
const H = 220
const PAD = { top: 12, right: 72, bottom: 24, left: 52 }

function niceTicks(min: number, max: number, count = 4): number[] {
  const span = max - min || 0.01
  const raw = span / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw
  const out: number[] = []
  for (let v = Math.ceil(min / step) * step; v <= max + 1e-12; v += step) out.push(Number(v.toFixed(10)))
  return out
}

export function LiftChart({
  series,
  variants,
  control,
  metricName,
}: {
  series: Map<string, SeriesPoint[]>
  variants: string[]
  control: string
  metricName: string
}) {
  const [hover, setHover] = useState<number | null>(null)

  const model = useMemo(() => {
    const entries = [...series.entries()].filter(([, pts]) => pts.length > 0)
    const dates = [...new Set(entries.flatMap(([, pts]) => pts.map((p) => p.date)))].sort()
    let lo = 0
    let hi = 0
    for (const [, pts] of entries) {
      for (const p of pts) {
        lo = Math.min(lo, p.low)
        hi = Math.max(hi, p.high)
      }
    }
    const pad = (hi - lo) * 0.08 || 0.005
    lo -= pad
    hi += pad
    const iw = W - PAD.left - PAD.right
    const ih = H - PAD.top - PAD.bottom
    const x = (i: number) => PAD.left + (dates.length <= 1 ? iw / 2 : (i / (dates.length - 1)) * iw)
    const y = (v: number) => PAD.top + ((hi - v) / (hi - lo)) * ih
    const index = new Map(dates.map((d, i) => [d, i]))
    return { entries, dates, lo, hi, x, y, index }
  }, [series])

  if (model.dates.length === 0) {
    return <p className="text-[13px] text-ink-muted">No daily data yet.</p>
  }

  const { entries, dates, x, y, index } = model
  const ticks = niceTicks(model.lo, model.hi)

  function onMove(e: React.MouseEvent<SVGSVGElement>) {
    const rect = e.currentTarget.getBoundingClientRect()
    const px = ((e.clientX - rect.left) / rect.width) * W
    const iw = W - PAD.left - PAD.right
    const i = Math.round(((px - PAD.left) / iw) * (dates.length - 1))
    setHover(Math.max(0, Math.min(dates.length - 1, i)))
  }

  const hoverDate = hover === null ? null : dates[hover]

  return (
    <figure className="relative">
      <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-ink-soft" aria-label="Legend">
        {entries.map(([variant]) => (
          <span key={variant} className="flex items-center gap-1.5">
            <span className="h-0.5 w-4 rounded" style={{ background: variantColor(variants, control, variant) }} />
            {variant} vs {control}
          </span>
        ))}
        <span className="text-ink-muted">shaded: confidence interval</span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-auto w-full"
        role="img"
        aria-label={`Cumulative lift in ${metricName} over time`}
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
      >
        {ticks.map((t) => (
          <g key={t}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} stroke="var(--color-line)" strokeWidth={t === 0 ? 1.25 : 0.75} />
            <text x={PAD.left - 6} y={y(t)} dy="0.32em" textAnchor="end" fontSize={11} fill="var(--color-ink-muted)">
              {formatLift(t)}
            </text>
          </g>
        ))}
        <text x={PAD.left} y={H - 6} fontSize={11} fill="var(--color-ink-muted)">
          {dates[0]}
        </text>
        <text x={W - PAD.right} y={H - 6} fontSize={11} textAnchor="end" fill="var(--color-ink-muted)">
          {dates[dates.length - 1]}
        </text>

        {entries.map(([variant, pts]) => {
          const color = variantColor(variants, control, variant)
          const top = pts.map((p) => `${x(index.get(p.date)!)},${y(p.high)}`)
          const bottom = pts.map((p) => `${x(index.get(p.date)!)},${y(p.low)}`).reverse()
          const line = pts.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(index.get(p.date)!)},${y(p.lift)}`).join(' ')
          const last = pts[pts.length - 1]
          return (
            <g key={variant}>
              <polygon points={[...top, ...bottom].join(' ')} fill={color} opacity={0.14} />
              <path d={line} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
              <text x={x(index.get(last.date)!) + 6} y={y(last.lift)} dy="0.32em" fontSize={11} fill="var(--color-ink-soft)">
                {variant}
              </text>
            </g>
          )
        })}

        {hover !== null && (
          <g pointerEvents="none">
            <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={H - PAD.bottom} stroke="var(--color-ink-muted)" strokeWidth={1} />
            {entries.map(([variant, pts]) => {
              const p = pts.find((pt) => pt.date === hoverDate)
              if (!p) return null
              return (
                <circle
                  key={variant}
                  cx={x(hover)}
                  cy={y(p.lift)}
                  r={4}
                  fill={variantColor(variants, control, variant)}
                  stroke="var(--color-surface)"
                  strokeWidth={2}
                />
              )
            })}
          </g>
        )}
      </svg>
      {hover !== null && (
        <div
          role="tooltip"
          className="pointer-events-none absolute top-8 rounded-md border bg-surface px-2.5 py-1.5 text-[12px] shadow-lg"
          style={{
            left: `${(x(hover) / W) * 100}%`,
            transform: hover > dates.length / 2 ? 'translateX(calc(-100% - 8px))' : 'translateX(8px)',
          }}
        >
          <p className="font-medium text-ink">{hoverDate}</p>
          {entries.map(([variant, pts]) => {
            const p = pts.find((pt) => pt.date === hoverDate)
            if (!p) return null
            return (
              <p key={variant} className="whitespace-nowrap text-ink-soft">
                <span
                  className="mr-1.5 inline-block h-2 w-2 rounded-full"
                  style={{ background: variantColor(variants, control, variant) }}
                />
                {variant}: <span className="tabular-nums text-ink">{formatLift(p.lift)}</span>{' '}
                <span className="tabular-nums">{formatCI(p.low, p.high)}</span>
              </p>
            )
          })}
        </div>
      )}
    </figure>
  )
}
