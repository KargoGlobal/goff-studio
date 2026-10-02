// Pastel Power Rangers: green, red, blue, yellow, pink, purple, orange.
const HUES = [150, 0, 215, 48, 330, 270, 25]

// ponytail: cycles after 7 variations; shift lightness per lap if flags ever need more.
export function variationColor(index: number): string {
  return `hsl(${HUES[index % HUES.length]} var(--variation-s) var(--variation-l))`
}
