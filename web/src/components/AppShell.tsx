import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Moon, Sun, LogOut, Plus, ShieldAlert, Flag, Layers, Menu, Users, X } from 'lucide-react'
import { Logo } from '@/components/Logo'
import { cn } from '@/lib/cn'
import { api, type Environment } from '@/lib/api'
import { Badge } from '@/components/ui/primitives'
import { NewEnvironmentDialog } from '@/components/NewEnvironmentDialog'
import { MOBILE_QUERY, useMediaQuery, useOverlay } from '@/hooks/useOverlay'

function useTheme() {
  const [dark, setDark] = useState(() => localStorage.getItem('goff-studio-theme') === 'dark')

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('goff-studio-theme', dark ? 'dark' : 'light')
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#4a5f5e' : '#87a8a7')
  }, [dark])

  return { dark, toggle: () => setDark((d) => !d) }
}

export function AppShell({
  user,
  environments,
  canCreateEnvironments,
  hasTeams = false,
  currentEnv,
  children,
}: {
  user: { name: string; email: string }
  environments: Environment[]
  canCreateEnvironments: boolean
  hasTeams?: boolean
  currentEnv?: string
  children: ReactNode
}) {
  const { dark, toggle } = useTheme()
  const [newEnv, setNewEnv] = useState(false)
  const location = useLocation()
  const active = environments.find((e) => e.name === currentEnv)
  const onTeams = Boolean(currentEnv) && location.pathname === `/env/${currentEnv}/teams`
  const isMobile = useMediaQuery(MOBILE_QUERY)
  const [navOpenAt, setNavOpenAt] = useState<string | null>(null)
  const navOpen = navOpenAt === location.pathname
  const drawerOpen = isMobile && navOpen
  const menuButton = useRef<HTMLButtonElement>(null)
  const closeButton = useRef<HTMLButtonElement>(null)
  const closeNav = useCallback(() => setNavOpenAt(null), [])

  useOverlay(drawerOpen, closeNav)

  useEffect(() => {
    window.scrollTo(0, 0)
  }, [location.pathname])

  const wasOpen = useRef(false)
  useEffect(() => {
    if (drawerOpen) closeButton.current?.focus()
    else if (wasOpen.current) menuButton.current?.focus()
    wasOpen.current = drawerOpen
  }, [drawerOpen])

  return (
    <div className="flex h-screen overflow-hidden max-md:h-auto max-md:min-h-dvh max-md:flex-col max-md:overflow-visible">
      <header className="sticky top-0 z-30 flex items-center gap-2 border-b border-[color:var(--color-sidebar-line)] bg-[color:var(--color-sidebar)] pb-1.5 pl-[max(0.5rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))] pt-[max(0.375rem,env(safe-area-inset-top))] text-[color:var(--color-sidebar-ink)] md:hidden">
        <button
          ref={menuButton}
          type="button"
          onClick={() => setNavOpenAt(location.pathname)}
          aria-label="Open navigation"
          aria-expanded={drawerOpen}
          aria-controls="studio-nav"
          className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-md transition-colors hover:bg-[color:var(--color-sidebar-hover)]"
        >
          <Menu className="h-5 w-5" />
        </button>
        <Logo className="h-8 w-8 shrink-0" />
        <span className="text-[17px] font-bold tracking-tight">Studio</span>
        {active && (
          <span className="ml-auto flex min-w-0 items-center gap-1.5 text-[13px] font-medium text-[color:var(--color-sidebar-ink-muted)]">
            {active.protected && <ShieldAlert className="h-3.5 w-3.5 shrink-0 text-[color:var(--color-flare)]" />}
            <span className="truncate">{active.name}</span>
          </span>
        )}
      </header>

      <div
        className={cn(
          'fixed inset-0 z-40 bg-black/40 transition-opacity motion-reduce:transition-none md:hidden',
          drawerOpen ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
        onClick={closeNav}
        aria-hidden="true"
      />

      <aside
        id="studio-nav"
        inert={isMobile && !navOpen}
        aria-label="Navigation"
        className={cn(
          'sticky top-0 flex h-screen w-60 shrink-0 flex-col overflow-y-auto border-r border-[color:var(--color-sidebar-line)] bg-[color:var(--color-sidebar)] text-[color:var(--color-sidebar-ink)]',
          'max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-50 max-md:h-dvh max-md:w-72 max-md:max-w-[85vw] max-md:overscroll-contain max-md:pl-[env(safe-area-inset-left)] max-md:pt-[env(safe-area-inset-top)] max-md:transition-[translate,box-shadow] max-md:duration-200 max-md:motion-reduce:transition-none',
          drawerOpen ? 'max-md:translate-x-0 max-md:shadow-xl' : 'max-md:-translate-x-full',
        )}
      >
        <button
          ref={closeButton}
          type="button"
          onClick={closeNav}
          aria-label="Close navigation"
          className="absolute right-2 top-[max(0.5rem,env(safe-area-inset-top))] inline-flex h-11 w-11 items-center justify-center rounded-md transition-colors hover:bg-[color:var(--color-sidebar-hover)] md:hidden"
        >
          <X className="h-5 w-5" />
        </button>
        <Link
          to={currentEnv ? `/env/${currentEnv}` : '/'}
          onClick={closeNav}
          aria-label="Studio home: all flags"
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
            const isActive = env.name === currentEnv && !onTeams
            return (
              <Link
                key={env.name}
                to={`/env/${env.name}`}
                onClick={closeNav}
                className={cn(
                  'group flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors max-md:min-h-11 max-md:text-[15px]',
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
                <span className="flex-1 truncate">{env.name}</span>
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

          {canCreateEnvironments && (
            <button
              type="button"
              onClick={() => {
                closeNav()
                setNewEnv(true)
              }}
              className="mt-1 flex items-center gap-2 rounded-md px-3 py-1.5 text-left text-[13px] max-md:min-h-11 max-md:text-sm text-[color:var(--color-sidebar-ink-muted)] transition-colors hover:bg-[color:var(--color-sidebar-hover)] hover:text-[color:var(--color-sidebar-ink)]"
            >
              <Plus className="h-3.5 w-3.5" />
              New environment
            </button>
          )}

          {hasTeams && currentEnv && (
            <Link
              to={`/env/${currentEnv}/teams`}
              onClick={closeNav}
              aria-current={onTeams ? 'page' : undefined}
              className={cn(
                'mt-4 flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors max-md:min-h-11 max-md:text-[15px]',
                onTeams
                  ? 'bg-[color:var(--color-sidebar-active)] text-[color:var(--color-sidebar-active-ink)] font-medium shadow-sm'
                  : 'text-[color:var(--color-sidebar-ink)] hover:bg-[color:var(--color-sidebar-hover)]',
              )}
            >
              <Users className="h-3.5 w-3.5 shrink-0" />
              Teams
            </Link>
          )}
        </nav>

        <div className="shrink-0 px-3 pb-6 pt-4 max-md:pb-[max(1.5rem,env(safe-area-inset-bottom))]">
          <div className="mx-auto mb-3 flex w-fit items-center gap-1 rounded-full bg-[color:var(--color-sidebar-hover)] p-1">
            <button
              type="button"
              onClick={() => dark && toggle()}
              aria-label="Light mode"
              aria-pressed={!dark}
              className={cn(
                'inline-flex h-8 w-8 items-center justify-center rounded-full transition-colors max-md:h-11 max-md:w-11',
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
                'inline-flex h-8 w-8 items-center justify-center rounded-full transition-colors max-md:h-11 max-md:w-11',
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
            className="mx-auto flex h-8 w-full max-w-40 items-center max-md:h-11 justify-center gap-2 rounded-md text-[13px] font-medium text-[color:var(--color-sidebar-ink)] transition-colors hover:bg-[color:var(--color-sidebar-hover)]"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </button>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-y-auto bg-[color:var(--color-page)] text-[color:var(--color-page-ink)] max-md:overflow-visible">
        {active?.protected ? (
          <div className="flex items-center gap-2 border-b border-[color:var(--color-flare)]/40 bg-[color:var(--color-flare-soft)] px-6 py-2 text-[12.5px] text-[color:var(--color-flare)] max-md:pl-[max(1rem,env(safe-area-inset-left))] max-md:pr-[max(1rem,env(safe-area-inset-right))]">
            <ShieldAlert className="h-3.5 w-3.5 max-md:shrink-0" />
            <span>
              You are editing <strong>{active.name}</strong>. Changes go live for real users.
            </span>
          </div>
        ) : (
          // Spacer matches the banner's rendered height so pages don't shift up
          // when moving between protected and non-protected environments.
          <div className="h-9 max-md:hidden" aria-hidden />
        )}
        <div
          key={location.pathname}
          className="px-6 py-6 max-md:pb-[max(1.5rem,env(safe-area-inset-bottom))] max-md:pl-[max(1rem,env(safe-area-inset-left))] max-md:pr-[max(1rem,env(safe-area-inset-right))] max-md:pt-4"
        >
          {children}
        </div>
      </main>

      {canCreateEnvironments && <NewEnvironmentDialog open={newEnv} onClose={() => setNewEnv(false)} />}
    </div>
  )
}

export function EnvBadge({ env }: { env: Environment }) {
  return (
    <Badge tone={env.protected ? 'warn' : 'neutral'}>
      {env.name}
    </Badge>
  )
}
