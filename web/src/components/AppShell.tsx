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
        <a
          href="https://gofeatureflag.org/"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="GO Feature Flag (opens in a new tab)"
          className="flex flex-col items-center gap-2 rounded-md px-4 py-5 transition-opacity hover:opacity-90"
        >
          <Logo className="h-20 w-20 shrink-0" />
          <div className="text-center leading-tight">
            <p className="text-[22px] font-bold tracking-tight text-[color:var(--color-sidebar-ink)]">Studio</p>
            <p className="text-[12px] font-semibold uppercase tracking-wider text-[color:var(--color-sidebar-ink-muted)]">
              GO Feature Flag
            </p>
          </div>
        </a>

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

        <div className="shrink-0 px-3 pb-6 pt-4">
          <div className="mx-auto mb-3 flex w-fit items-center gap-1 rounded-full bg-[color:var(--color-sidebar-hover)] p-1">
            <button
              type="button"
              onClick={() => dark && toggle()}
              aria-label="Light mode"
              aria-pressed={!dark}
              className={cn(
                'inline-flex h-8 w-8 items-center justify-center rounded-full transition-colors',
                !dark
                  ? 'bg-[color:var(--color-sidebar-active)] text-[color:var(--color-sidebar-active-ink)]'
                  : 'text-[color:var(--color-sidebar-ink-muted)] hover:text-[color:var(--color-sidebar-ink)]',
              )}
            >
              <Sun className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => !dark && toggle()}
              aria-label="Dark mode"
              aria-pressed={dark}
              className={cn(
                'inline-flex h-8 w-8 items-center justify-center rounded-full transition-colors',
                dark
                  ? 'bg-[color:var(--color-sidebar-active)] text-[color:var(--color-sidebar-active-ink)]'
                  : 'text-[color:var(--color-sidebar-ink-muted)] hover:text-[color:var(--color-sidebar-ink)]',
              )}
            >
              <Moon className="h-4 w-4" />
            </button>
          </div>
          <div className="mx-auto mb-4 h-px w-48 bg-[color:var(--color-sidebar-line)]" aria-hidden />
          <div className="mb-3 min-w-0 text-center">
            <p className="truncate text-[15px] font-semibold text-[color:var(--color-sidebar-ink)]">{user.name}</p>
            <p className="truncate text-[12.5px] text-[color:var(--color-sidebar-ink-muted)]">{user.email}</p>
          </div>
          <button
            type="button"
            aria-label="Sign out"
            onClick={async () => {
              await api.logout()
              window.location.href = '/'
            }}
            className="mx-auto flex h-8 w-full max-w-40 items-center justify-center gap-2 rounded-md text-[13px] font-medium text-[color:var(--color-sidebar-ink)] transition-colors hover:bg-[color:var(--color-sidebar-hover)]"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </button>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-y-auto bg-[color:var(--color-page)] text-[color:var(--color-page-ink)]">
        {active?.protected ? (
          <div className="flex items-center gap-2 border-b border-[color:var(--color-flare)]/40 bg-[color:var(--color-flare-soft)] px-6 py-2 text-[12.5px] text-[color:var(--color-flare)]">
            <ShieldAlert className="h-3.5 w-3.5" />
            <span>
              You are editing <strong>{active.display}</strong>. Changes go live for real users.
            </span>
          </div>
        ) : (
          // Spacer matches the banner's rendered height so pages don't shift up
          // when moving between protected and non-protected environments.
          <div className="h-9" aria-hidden />
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
