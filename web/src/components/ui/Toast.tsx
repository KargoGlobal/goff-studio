import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { cn } from '@/lib/cn'
import { CheckCircle2, XCircle } from 'lucide-react'

type Toast = { id: number; message: string; tone: 'ok' | 'error' }

const ToastContext = createContext<(message: string, tone?: 'ok' | 'error') => void>(() => {})

export function useToast() {
  return useContext(ToastContext)
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])

  const push = useCallback((message: string, tone: 'ok' | 'error' = 'ok') => {
    const id = Date.now() + Math.random()
    setToasts((prev) => [...prev, { id, message, tone }])
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 6000)
  }, [])

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-[60] flex w-80 flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            role="status"
            className={cn(
              'flex items-start gap-2.5 rounded-lg border px-3.5 py-3 text-[13px] shadow-lg',
              t.tone === 'ok' ? 'border-ok bg-ok-soft text-ok' : 'border-danger bg-danger-soft text-danger',
            )}
          >
            {t.tone === 'ok' ? (
              <CheckCircle2 className="mt-px h-4 w-4 shrink-0" />
            ) : (
              <XCircle className="mt-px h-4 w-4 shrink-0" />
            )}
            <span className="text-ink">{t.message}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
