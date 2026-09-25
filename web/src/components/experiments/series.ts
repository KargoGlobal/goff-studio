const SLOTS = ['var(--color-series-1)', 'var(--color-series-2)', 'var(--color-series-3)', 'var(--color-series-4)']

// Colour follows the variant's position in the registry, never its rank in a table.
export function variantColor(variants: string[], control: string, variant: string): string {
  if (variant === control) return 'var(--color-ink-muted)'
  const treatments = variants.filter((v) => v !== control)
  const i = treatments.indexOf(variant)
  return i >= 0 && i < SLOTS.length ? SLOTS[i] : 'var(--color-ink-soft)'
}
