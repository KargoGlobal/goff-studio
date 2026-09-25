import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import { Badge } from '@/components/ui/primitives'
import { RECOMMENDATION_LABEL, recommendationTone, statusTone } from '@/lib/experiments'
import type { ExperimentStatus, Recommendation } from '@/lib/experimentTypes'

export function StatusBadge({ status }: { status: ExperimentStatus }) {
  return <Badge tone={statusTone(status)}>{status}</Badge>
}

export function RecommendationBadge({ value }: { value?: Recommendation }) {
  if (!value) return <span className="text-ink-muted">—</span>
  return <Badge tone={recommendationTone(value)}>{RECOMMENDATION_LABEL[value] ?? value}</Badge>
}

export function SRMBadge({ flag }: { flag: boolean }) {
  return flag ? (
    <Badge tone="danger" className="gap-1">
      <AlertTriangle className="h-3 w-3" aria-hidden />
      SRM
    </Badge>
  ) : (
    <Badge tone="ok" className="gap-1">
      <CheckCircle2 className="h-3 w-3" aria-hidden />
      OK
    </Badge>
  )
}

export function SampleBadge() {
  return (
    <Badge tone="warn" title="No analysis service is configured; these numbers are generated sample data.">
      sample data
    </Badge>
  )
}
