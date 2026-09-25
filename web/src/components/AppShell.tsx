import { useEffect, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Moon, Sun, LogOut, Plus, ShieldAlert, Flag, Layers } from 'lucide-react'
import { Logo } from '@/components/Logo'
import { cn } from '@/lib/cn'
import { api, type Environment } from '@/lib/api'
import { Badge } from '@/components/ui/primitives'
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
      <aside className="sticky top-0 flex h-screen w-60 shrink-0 flex-col overflow-y-auto border-r border-[color:var(--color-sidebar-line)] bg-[color:var(--color-sidebar)] text-[color:var(--color-sidebar-ink)]">
        <Link
          to="/"
          aria-label="GO Feature Flag Studio home"
          className="flex flex-col items-center gap-2 rounded-md px-4 py-5 transition-opacity hover:opacity-90"
        >
          <Logo className="h-20 w-20 shrink-0" />
          <div className="text-center leading-tight">
            <p className="text-[22px] font-bold tracking-tight text-[color:var(--color-sidebar-ink)]">Studio</p>
            <p className="text-[12px] font-semibold uppercase tracking-wider text-[color:var(--color-sidebar-ink-muted)]">
              GO Feature Flag
            </p>
          </div>
        </Link>

        <nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2">
          <p className="flex items-center gap-1.5 px-3 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-wide text-[color:var(--color-sidebar-ink-muted)]">
            <Layers className="h-3 w-3" />
            Environments
          </p>
          {environments.map((env) => {
            const isActive = env.name === currentEnv
            return (
              <Link
                key={env.name}
                to={`/env/${env.name}`}
                className={cn(
                  'group flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors',
                  isActive
                    ? 'bg-[color:var(--color-sidebar-active)] text-[color:var(--color-sidebar-active-ink)] font-medium shadow-sm'
                    : 'text-[color:var(--color-sidebar-ink)] hover:bg-[color:var(--color-sidebar-hover)]',
                )}
              >
                <Flag
                  className={cn(
                    'h-3.5 w-3.5 shrink-0',
                    isActive
                      ? 'text-[color:var(--color-sidebar-active-ink)]'
                      : 'text-[color:var(--color-sidebar-ink)]',
                  )}
                />
                <span className="flex-1 truncate">{env.display}</span>
                {env.protected && (
                  <ShieldAlert
                    className={cn(
                      'h-3.5 w-3.5 shrink-0',
                      isActive
                        ? 'text-[color:var(--color-sidebar-active-ink)]'
                        : 'text-[color:var(--color-flare)]',
                    )}
                  />
                )}
              </Link>
            )
          })}

          <button
            type="button"
            onClick={() => setNewEnv(true)}
            className="mt-1 flex items-center gap-2 rounded-md px-3 py-1.5 text-left text-[13px] text-[color:var(--color-sidebar-ink-muted)] transition-colors hover:bg-[color:var(--color-sidebar-hover)] hover:text-[color:var(--color-sidebar-ink)]"
          >
            <Plus className="h-3.5 w-3.5" />
            New environment
          </button>
        </nav>

        <div className="shrink-0 border-t border-[color:var(--color-sidebar-line)] px-4 py-3">
          <div className="mb-2">
            <p className="truncate text-[13px] font-medium text-[color:var(--color-sidebar-ink)]">{user.name}</p>
            <p className="truncate text-[11px] text-[color:var(--color-sidebar-ink-muted)]">{user.email}</p>
          </div>
          <div className="flex gap-1">
            <button
              type="button"
              onClick={toggle}
              aria-label="Toggle theme"
              className="inline-flex h-8 w-8 items-center justify-center rounded-md text-[color:var(--color-sidebar-ink)] transition-colors hover:bg-[color:var(--color-sidebar-hover)]"
            >
              {dark ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
            </button>
            <button
              type="button"
              aria-label="Sign out"
              onClick={async () => {
                await api.logout()
                window.location.href = '/'
              }}
              className="inline-flex h-8 w-8 items-center justify-center rounded-md text-[color:var(--color-sidebar-ink)] transition-colors hover:bg-[color:var(--color-sidebar-hover)]"
            >
              <LogOut className="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-y-auto bg-[color:var(--color-page)] text-[color:var(--color-page-ink)]">
        {active?.protected && (
          <div className="flex items-center gap-2 border-b border-[color:var(--color-flare)]/40 bg-[color:var(--color-flare-soft)] px-6 py-2 text-[12.5px] text-[color:var(--color-flare)]">
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
