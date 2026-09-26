import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, ArrowRight } from 'lucide-react'
import {
  api,
  ApiError,
  type CompareSide,
  type DiffResult,
  type Environment,
  type PromotePayload,
} from '@/lib/api'
import { useCompare, usePromote } from '@/hooks/useFlags'
import { Badge, Button, Card, Spinner } from '@/components/ui/primitives'
import { Select } from '@/components/ui/Select'
import { ReviewDialog } from '@/components/ReviewDialog'
import { useToast } from '@/components/ui/Toast'
import { ScheduleBadge } from '@/components/ScheduleBadge'

const FIELD_LABELS: Record<string, string> = {
  variations: 'Variations',
  default: 'Default rule',
  rules: 'Targeting rules',
  experimentation: 'Schedule',
  metadata: 'Metadata',
  enabled: 'On/off state',
}

const SAFE_FIELDS = ['variations', 'default', 'rules', 'experimentation', 'metadata']

function SideCard({ side, label }: { side: CompareSide; label: string }) {
  return (
    <Card className="flex-1 p-3">
      <p className="text-[11px] font-medium uppercase tracking-wide text-ink-muted">{label}</p>
      <p className="mt-0.5 flex items-center gap-2 text-[13px] font-medium text-ink">
        {side.display}
        {side.present && (
          <>
            <Badge tone={side.enabled ? 'ok' : 'neutral'}>{side.enabled ? 'on' : 'off'}</Badge>
            {side.flag && <ScheduleBadge flag={side.flag} />}
          </>
        )}
      </p>
      {side.present ? (
        <>
          <p className="mt-1.5 text-[12.5px] text-ink-soft">{side.summary}</p>
          {side.team && <p className="mt-1 text-[11.5px] text-ink-muted">Team: {side.team}</p>}
        </>
      ) : (
        <p className="mt-1.5 text-[12.5px] text-ink-muted">Not defined in this environment.</p>
      )}
    </Card>
  )
}

export function ComparePage({ environments }: { environments: Environment[] }) {
  const { env = '', key = '' } = useParams()
  const navigate = useNavigate()
  const toast = useToast()

  const others = environments.filter((e) => e.name !== env)
  const [target, setTarget] = useState(others[0]?.name ?? '')
  const [fields, setFields] = useState<string[]>(SAFE_FIELDS)

  const { data, isLoading, error } = useCompare(key, env, target)
  const promote = usePromote(key)

  const [reviewing, setReviewing] = useState(false)
  const [diff, setDiff] = useState<DiffResult | undefined>()
  const [loadingDiff, setLoadingDiff] = useState(false)

  const targetEnv = environments.find((e) => e.name === target)
  const payload: PromotePayload = useMemo(
    () => ({ from: env, to: target, fields, fileSha: data?.to.fileSha }),
    [env, target, fields, data?.to.fileSha],
  )

  async function openReview() {
    setReviewing(true)
    setDiff(undefined)
    setLoadingDiff(true)
    try {
      setDiff(await api.promoteDiff(key, payload))
    } catch (e) {
      toast(e instanceof ApiError ? e.message : 'Could not build the diff', 'error')
      setReviewing(false)
    } finally {
      setLoadingDiff(false)
    }
  }

  async function confirm() {
    try {
      await promote.mutateAsync(payload)
      toast(`Promoted ${key} to ${targetEnv?.display ?? target}`)
      setReviewing(false)
      navigate(`/env/${target}/flags/${encodeURIComponent(key)}`)
    } catch (e) {
      toast(e instanceof ApiError ? e.message : 'Could not promote', 'error')
    }
  }

  const toggle = (field: string) =>
    setFields((prev) =>
      prev.includes(field) ? prev.filter((f) => f !== field) : [...prev, field],
    )

  if (others.length === 0) {
    return (
      <div className="space-y-3">
        <Link to={`/env/${env}/flags/${encodeURIComponent(key)}`} className="text-[13px] text-brand">
          Back to {key}
        </Link>
        <p className="text-[13px] text-ink-muted">
          You only have access to one environment, so there is nothing to compare.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <Link
          to={`/env/${env}/flags/${encodeURIComponent(key)}`}
          className="inline-flex items-center gap-1.5 text-[13px] text-ink-muted hover:text-ink"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to {key}
        </Link>
        <h1 className="mt-1 font-mono text-lg font-semibold text-ink">{key}</h1>
      </div>

      <div className="flex items-center gap-2">
        <span className="text-[13px] text-ink-muted">Compare with</span>
        <div className="w-56">
          <Select
            value={target}
            onChange={(v) => setTarget(v)}
            ariaLabel="Target environment"
            options={others.map((e) => ({ value: e.name, label: e.display }))}
          />
        </div>
      </div>

      {isLoading && <Spinner />}
      {error && (
        <p className="text-[13px] text-danger">
          {error instanceof ApiError ? error.message : 'Could not compare these environments'}
        </p>
      )}

      {data && (
        <>
          <div className="flex items-stretch gap-2">
            <SideCard side={data.from} label="Source" />
            <div className="flex items-center text-ink-muted">
              <ArrowRight className="h-4 w-4" />
            </div>
            <SideCard side={data.to} label="Target" />
          </div>

          <Card className="p-4">
            <h2 className="text-[13px] font-semibold uppercase tracking-wide text-ink-muted">
              Differences
            </h2>
            {data.differs.length === 0 ? (
              <p className="mt-1.5 text-[13px] text-ink-soft">
                These environments already match.
              </p>
            ) : (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {data.differs.map((f) => (
                  <Badge key={f} tone="warn">
                    {FIELD_LABELS[f] ?? f}
                  </Badge>
                ))}
              </div>
            )}

            {data.diff && (
              <details className="mt-3">
                <summary className="cursor-pointer text-[12.5px] text-ink-muted hover:text-ink">
                  Show the difference
                </summary>
                <pre className="mt-2 overflow-x-auto rounded-md border border-[color:var(--color-brand)] bg-surface p-2.5 text-[12px] leading-relaxed">
                  {data.diff}
                </pre>
              </details>
            )}
          </Card>

          <Card className="p-4">
            <h2 className="text-[13px] font-semibold uppercase tracking-wide text-ink-muted">
              Promote to {data.to.display}
            </h2>
            <p className="mt-1 text-[12px] text-ink-muted">
              Copies the settings you pick from {data.from.display}. A flag that does not exist yet
              is created turned off.
            </p>

            {data.blockers.length > 0 && (
              <p role="alert" className="mt-2 text-[12.5px] text-danger">
                {data.blockers.join('; ')}
              </p>
            )}

            <div className="mt-3 space-y-1.5">
              {Object.keys(FIELD_LABELS).map((field) => (
                <label key={field} className="flex items-center gap-2 text-[13px] text-ink">
                  <input
                    type="checkbox"
                    checked={fields.includes(field)}
                    onChange={() => toggle(field)}
                    className="h-3.5 w-3.5"
                  />
                  {FIELD_LABELS[field]}
                  {field === 'enabled' && (
                    <span className="text-[11.5px] text-warn">
                      changes whether the flag is live
                    </span>
                  )}
                  {data.differs.includes(field) && <Badge tone="warn">differs</Badge>}
                </label>
              ))}
            </div>

            <div className="mt-3 flex items-center gap-2">
              <Button
                size="sm"
                disabled={
                  fields.length === 0 || data.blockers.length > 0 || !data.to.writable
                }
                onClick={() => void openReview()}
              >
                Review promotion
              </Button>
              {!data.to.writable && (
                <span className="text-[12px] text-ink-muted">
                  You cannot write to {data.to.display}.
                </span>
              )}
            </div>
          </Card>
        </>
      )}

      <ReviewDialog
        open={reviewing}
        onClose={() => setReviewing(false)}
        onConfirm={() => void confirm()}
        title={`Promote ${key} to ${targetEnv?.display ?? target}`}
        diff={diff}
        loading={loadingDiff}
        saving={promote.isPending}
        protectedEnv={Boolean(targetEnv?.protected)}
        envName={target}
      />
    </div>
  )
}
