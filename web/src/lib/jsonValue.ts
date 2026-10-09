export const INLINE_LIMIT = 60

export interface JsonSummary {
  inline: string
  expandable: boolean
  size?: string
}

function sizeOf(value: unknown): string | undefined {
  if (Array.isArray(value)) return `${value.length} item${value.length === 1 ? '' : 's'}`
  if (value !== null && typeof value === 'object') {
    const n = Object.keys(value).length
    return `${n} key${n === 1 ? '' : 's'}`
  }
  return undefined
}

export function summarizeJson(value: unknown): JsonSummary {
  const inline = JSON.stringify(value) ?? String(value)
  if (inline.length <= INLINE_LIMIT) return { inline, expandable: false }
  return { inline, expandable: true, size: sizeOf(value) }
}

export function prettyJson(value: unknown): string {
  return JSON.stringify(value, null, 2) ?? String(value)
}
