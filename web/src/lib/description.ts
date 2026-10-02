import type { Flag } from '@/lib/api'

export const MAX_DESCRIPTION = 500

export function descriptionOf(flag: Pick<Flag, 'metadata'>): string {
  const raw = flag.metadata?.description
  return typeof raw === 'string' ? raw.trim() : ''
}
