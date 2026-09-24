import { useEffect, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Moon, Sun, LogOut, Plus, ShieldAlert } from 'lucide-react'
import { Logo } from '@/components/Logo'
import { cn } from '@/lib/cn'
import { api, type Environment } from '@/lib/api'
import { Badge, Button } from '@/components/ui/primitives'
import { NewEnvironmentDialog } from '@/components/NewEnvironmentDialog'

function useTheme() {
  const [dark, setDark] = useState(() => localStorage.getItem('goff-studio-theme') === 'dark')

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('goff-studio-theme', dark ? 'dark' : 'light')
  }, [dark])

  return { dark, toggle: () => setDark((d) => !d) }
}

export function AppShell({
  user,
  environments,
  currentEnv,
  children,
}: {
  user: { name: string; email: string }
  environments: Environment[]
  currentEnv?: string
  children: ReactNode
}) {
  const { dark, toggle } = useTheme()
  const [newEnv, setNewEnv] = useState(false)
  const location = useLocation()
  const active = environments.find((e) => e.name === currentEnv)

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="sticky top-0 flex h-screen w-60 shrink-0 flex-col overflow-y-auto border-r bg-surface">
        <Link
          to="/"
          aria-label="GO Feature Flag Studio home"
          className="flex items-center gap-2.5 rounded-md px-5 py-4 transition-opacity hover:opacity-80"
        >
          <Logo className="h-6 w-6 text-brand" />
          <div className="leading-tight">
            <p className="text-[14px] font-bold tracking-tight">Studio</p>
            <p className="text-[10px] font-medium uppercase tracking-wider text-ink-muted">
              GO Feature Flag
            </p>
          </div>
        </Link>

        <nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2">
          <p className="px-3 pb-1 pt-3 text-[11px] font-medium uppercase tracking-wide text-ink-muted">
            Environments
          </p>
          {environments.map((env) => {
            const isActive = env.name === currentEnv
            return (
              <Link
                key={env.name}
                to={`/env/${env.name}`}
                className={cn(
                  'flex items-center justify-between rounded-md px-3 py-1.5 text-sm transition-colors',
                  isActive ? 'bg-brand-soft font-medium text-brand' : 'text-ink-soft hover:bg-canvas',
                )}
              >
                <span>{env.display}</span>
                {env.protected && <ShieldAlert className="h-3.5 w-3.5 text-warn" />}
              </Link>
            )
          })}

          <button
            type="button"
            onClick={() => setNewEnv(true)}
            className="mt-1 flex items-center gap-1.5 rounded-md px-3 py-1.5 text-left text-[13px] text-ink-muted hover:bg-canvas hover:text-ink"
          >
            <Plus className="h-3.5 w-3.5" />
            New environment
          </button>
        </nav>

        <div className="shrink-0 border-t px-4 py-3">
          <div className="mb-2">
            <p className="truncate text-[13px] font-medium text-ink">{user.name}</p>
            <p className="truncate text-[11px] text-ink-muted">{user.email}</p>
          </div>
          <div className="flex gap-1">
            <Button variant="ghost" size="sm" onClick={toggle} aria-label="Toggle theme">
              {dark ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-label="Sign out"
              onClick={async () => {
                await api.logout()
                window.location.href = '/'
              }}
            >
              <LogOut className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-y-auto">
        {active?.protected && (
          <div className="flex items-center gap-2 border-b border-warn bg-warn-soft px-6 py-2 text-[12.5px] text-warn">
            <ShieldAlert className="h-3.5 w-3.5" />
            <span>
              You are editing <strong>{active.display}</strong>. Changes go live for real users.
            </span>
          </div>
        )}
        <div key={location.pathname} className="px-6 py-6">
          {children}
        </div>
      </main>

      <NewEnvironmentDialog open={newEnv} onClose={() => setNewEnv(false)} />
    </div>
  )
}

export function EnvBadge({ env }: { env: Environment }) {
  return (
    <Badge tone={env.protected ? 'warn' : 'neutral'}>
      {env.display}
    </Badge>
  )
}
