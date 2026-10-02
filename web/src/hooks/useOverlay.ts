import { useEffect, useSyncExternalStore } from 'react'

export const MOBILE_QUERY = '(max-width: 47.99rem)'

export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (notify) => {
      const mql = window.matchMedia(query)
      mql.addEventListener('change', notify)
      return () => mql.removeEventListener('change', notify)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}

let locks = 0

export function useOverlay(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  useEffect(() => {
    if (!open) return
    if (locks++ === 0) document.body.style.overflow = 'hidden'
    return () => {
      if (--locks === 0) document.body.style.overflow = ''
    }
  }, [open])
}
