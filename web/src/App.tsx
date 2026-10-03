import { lazy, Suspense, useState } from 'react'
import { Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { Logo } from '@/components/Logo'
import { useMe } from '@/hooks/useFlags'
import { ApiError } from '@/lib/api'
import { AppShell } from '@/components/AppShell'
import { FlagListPage } from '@/pages/FlagListPage'
import { FlagDetailPage } from '@/pages/FlagDetailPage'
import { ComparePage } from '@/pages/ComparePage'
import { CreateFlagPage } from '@/pages/CreateFlagPage'
import { NewEnvironmentDialog } from '@/components/NewEnvironmentDialog'
import { Button, Card, Spinner } from '@/components/ui/primitives'

const TeamsPage = lazy(() => import('@/pages/TeamsPage').then((m) => ({ default: m.TeamsPage })))

function SignIn() {
  return (
    <div className="flex min-h-screen items-center justify-center p-6 max-md:min-h-dvh max-md:p-4">
      <Card className="w-full max-w-sm p-8 text-center">
        <Logo className="mx-auto h-10 w-10 text-brand" />
        <h1 className="mt-4 text-[19px] font-bold tracking-tight">
          GO Feature Flag <span className="text-brand">Studio</span>
        </h1>
        <Button className="mt-6 w-full" onClick={() => (window.location.href = '/auth/login')}>
          Sign in
        </Button>
      </Card>
    </div>
  )
}

function NoAccess({ name }: { name: string }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6 max-md:min-h-dvh max-md:p-4">
      <Card className="w-full max-w-md p-8 text-center">
        <h1 className="text-lg font-semibold">No environments available</h1>
        <p className="mt-2 text-[13px] text-ink-soft">
          You are signed in as {name}, but there is no environment you can open. Either none has
          been created yet, or your groups do not grant access to one. Ask an administrator.
        </p>
      </Card>
    </div>
  )
}

function FirstEnvironment() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()

  return (
    <div className="flex min-h-screen items-center justify-center p-6 max-md:min-h-dvh max-md:p-4">
      <Card className="w-full max-w-md p-8 text-center">
        <h1 className="text-lg font-semibold">Create your first environment</h1>
        <p className="mt-2 text-[13px] text-ink-soft">
          Environments are the top-level folders of flag files in storage, and there are none yet.
        </p>
        <Button className="mt-6" onClick={() => setOpen(true)}>
          New environment
        </Button>
      </Card>
      <NewEnvironmentDialog
        open={open}
        onClose={() => setOpen(false)}
        onCreated={(name) => navigate(`/env/${name}`, { replace: true })}
      />
    </div>
  )
}

function Shell() {
  const { data: me } = useMe()
  const { env } = useParams()
  if (!me) return null

  return (
    <AppShell
      user={me}
      environments={me.environments}
      canCreateEnvironments={me.canCreateEnvironments}
      hasTeams={me.hasTeams}
      currentEnv={env}
    >
      <Routes>
        <Route path="" element={<FlagListPage environments={me.environments} />} />
        <Route path="flags/new" element={<CreateFlagPage environments={me.environments} />} />
        <Route path="flags/:key" element={<FlagDetailPage environments={me.environments} />} />
        <Route path="flags/:key/compare" element={<ComparePage environments={me.environments} />} />
        {me.hasTeams && (
          <Route
            path="teams"
            element={
              <Suspense fallback={<Spinner />}>
                <TeamsPage />
              </Suspense>
            }
          />
        )}
      </Routes>
    </AppShell>
  )
}

export function App() {
  const { data: me, isLoading, error } = useMe()

  if (isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (error instanceof ApiError && error.isUnauthenticated) return <SignIn />
  if (error) return <SignIn />
  if (!me) return <SignIn />
  if (me.environments.length === 0) {
    return me.canCreateEnvironments ? <FirstEnvironment /> : <NoAccess name={me.name} />
  }

  return (
    <Routes>
      <Route path="/" element={<Navigate to={`/env/${me.environments[0].name}`} replace />} />
      <Route path="/env/:env/*" element={<Shell />} />
      <Route path="*" element={<Navigate to={`/env/${me.environments[0].name}`} replace />} />
    </Routes>
  )
}
