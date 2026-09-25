import { ciGeometry, formatCI, formatLift, type Tone } from '@/lib/experiments'

const TONE: Record<Tone, string> = {
  good: 'var(--color-ok)',
  bad: 'var(--color-danger)',
  neutral: 'var(--color-ink-muted)',
}

export function CIBar({
  low,
  high,
  lift,
  domain,
  tone,
  width = 160,
  label,
}: {
  low: number
  high: number
  lift: number
  domain: number
  tone: Tone
  width?: number
  label?: string
}) {
  const height = 18
  const g = ciGeometry(low, high, lift, domain, width - 8)
  const color = TONE[tone]
  const text = `${label ? `${label}: ` : ''}lift ${formatLift(lift)}, interval ${formatCI(low, high)}`

  return (
    <svg
      width={width}
      height={height}
      viewBox={`-4 0 ${width} ${height}`}
      role="img"
      aria-label={text}
      className="block overflow-visible"
    >
      <title>{text}</title>
      <line x1={g.zero} x2={g.zero} y1={1} y2={height - 1} stroke="var(--color-line)" strokeWidth={1} />
      <rect
        x={g.low}
        y={height / 2 - 2}
        width={Math.max(g.high - g.low, 2)}
        height={4}
        rx={2}
        fill={color}
        opacity={tone === 'neutral' ? 0.55 : 0.8}
      />
      <circle cx={g.point} cy={height / 2} r={4} fill={color} stroke="var(--color-surface)" strokeWidth={2} />
    </svg>
  )
}
