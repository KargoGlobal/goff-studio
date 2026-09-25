import { lazy, Suspense, type ReactNode } from 'react'
import { Navigate, Outlet, Route, Routes, useParams } from 'react-router-dom'
import { Logo } from '@/components/Logo'
import { useMe } from '@/hooks/useFlags'
import { ApiError } from '@/lib/api'
import { AppShell } from '@/components/AppShell'
import { FlagListPage } from '@/pages/FlagListPage'
import { Button, Card, Spinner } from '@/components/ui/primitives'

// The flag list is the landing page; everything else loads on first visit.
const FlagDetailPage = lazy(() => import('@/pages/FlagDetailPage').then((m) => ({ default: m.FlagDetailPage })))
const CreateFlagPage = lazy(() => import('@/pages/CreateFlagPage').then((m) => ({ default: m.CreateFlagPage })))
const ExperimentListPage = lazy(() =>
  import('@/pages/experiments/ExperimentListPage').then((m) => ({ default: m.ExperimentListPage })),
)
const ExperimentDetailPage = lazy(() =>
  import('@/pages/experiments/ExperimentDetailPage').then((m) => ({ default: m.ExperimentDetailPage })),
)
const ExperimentFormPage = lazy(() =>
  import('@/pages/experiments/ExperimentFormPage').then((m) => ({ default: m.ExperimentFormPage })),
)
const MetricCatalogPage = lazy(() =>
  import('@/pages/experiments/MetricCatalogPage').then((m) => ({ default: m.MetricCatalogPage })),
)
const MetricEditPage = lazy(() =>
  import('@/pages/experiments/MetricCatalogPage').then((m) => ({ default: m.MetricEditPage })),
)

function Loading({ children }: { children: ReactNode }) {
  return (
    <Suspense
      fallback={
        <div className="flex items-center gap-2 text-ink-muted">
          <Spinner />
          <span>Loading…</span>
        </div>
      }
    >
      {children}
    </Suspense>
  )
}

function SignIn() {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
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
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8 text-center">
        <h1 className="text-lg font-semibold">No environments available</h1>
        <p className="mt-2 text-[13px] text-ink-soft">
          You are signed in as {name}, but your groups do not grant access to any environment yet.
          Ask an administrator to add you to a permission group.
        </p>
      </Card>
    </div>
  )
}

function Shell() {
  const { data: me } = useMe()
  const { env } = useParams()
  if (!me) return null

  return (
    <AppShell user={me} environments={me.environments} currentEnv={env}>
      <Loading>
        <Routes>
          <Route path="" element={<FlagListPage environments={me.environments} />} />
          <Route path="flags/new" element={<CreateFlagPage environments={me.environments} />} />
          <Route path="flags/:key" element={<FlagDetailPage environments={me.environments} />} />
        </Routes>
      </Loading>
    </AppShell>
  )
}

function ExperimentsLayout() {
  const { data: me } = useMe()
  if (!me) return null

  return (
    <AppShell user={me} environments={me.environments}>
      <Loading>
        <Outlet />
      </Loading>
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
  if (me.environments.length === 0) return <NoAccess name={me.name} />

  return (
    <Routes>
      <Route path="/" element={<Navigate to={`/env/${me.environments[0].name}`} replace />} />
      <Route path="/env/:env/*" element={<Shell />} />
      <Route element={<ExperimentsLayout />}>
        <Route path="/experiments" element={<ExperimentListPage />} />
        <Route path="/experiments/new" element={<ExperimentFormPage environments={me.environments} />} />
        <Route path="/experiments/:key" element={<ExperimentDetailPage />} />
        <Route path="/experiments/:key/edit" element={<ExperimentFormPage environments={me.environments} />} />
        <Route path="/metrics" element={<MetricCatalogPage />} />
        <Route path="/metrics/new" element={<MetricEditPage />} />
        <Route path="/metrics/:key" element={<MetricEditPage />} />
      </Route>
      <Route path="*" element={<Navigate to={`/env/${me.environments[0].name}`} replace />} />
    </Routes>
  )
}
