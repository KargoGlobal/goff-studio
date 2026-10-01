export type Split = Record<string, number>

const total = (split: Split) => Object.values(split).reduce((a, b) => a + b, 0)

function shareOut(names: string[], weights: number[], amount: number): Split {
  const sum = weights.reduce((a, b) => a + b, 0)
  const raw = names.map((_, i) => (sum > 0 ? (weights[i] / sum) * amount : amount / names.length))
  const out = raw.map(Math.floor)
  let left = amount - out.reduce((a, b) => a + b, 0)
  const byRemainder = raw.map((r, i) => [r - Math.floor(r), i] as const).sort((a, b) => b[0] - a[0])
  for (const [, i] of byRemainder) {
    if (left <= 0) break
    out[i] += 1
    left -= 1
  }
  return Object.fromEntries(names.map((n, i) => [n, out[i]]))
}

export function sumsTo100(split: Split): boolean {
  return Math.abs(total(split) - 100) < 1e-9
}

export function normalizeSplit(names: string[], split: Split): Split {
  const values = Object.fromEntries(names.map((n) => [n, Math.max(0, split[n] ?? 0)]))
  return sumsTo100(values) ? values : shareOut(names, Object.values(values), 100)
}

export function setShare(names: string[], split: Split, name: string, value: number): Split {
  const v = Math.min(100, Math.max(0, Math.round(value)))
  const others = names.filter((n) => n !== name)
  if (others.length === 0) return { [name]: 100 }
  const current = normalizeSplit(names, split)
  return { ...shareOut(others, others.map((n) => current[n]), 100 - v), [name]: v }
}
