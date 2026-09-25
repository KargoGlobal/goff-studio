import { useCallback } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Experiment, Metric, PowerRequest } from '@/lib/experimentTypes'

// Results change at most every few minutes upstream and the server caches for five.
const RESULTS_STALE = 5 * 60_000
const REGISTRY_STALE = 30_000

export const resultsKey = (key: string, asOf?: string) => ['experimentResults', key, asOf ?? ''] as const

export function useExperiments() {
  return useQuery({ queryKey: ['experiments'], queryFn: api.experiments, staleTime: REGISTRY_STALE })
}

export function useExperiment(key: string | undefined) {
  return useQuery({
    queryKey: ['experiment', key],
    queryFn: () => api.experiment(key!),
    enabled: Boolean(key),
    staleTime: REGISTRY_STALE,
  })
}

export function useExperimentResults(key: string | undefined, asOf?: string) {
  return useQuery({
    queryKey: resultsKey(key ?? '', asOf),
    queryFn: () => api.experimentResults(key!, asOf),
    enabled: Boolean(key),
    staleTime: RESULTS_STALE,
    gcTime: 15 * 60_000,
  })
}

export function usePrefetchResults() {
  const qc = useQueryClient()
  return useCallback(
    (key: string) =>
      void qc.prefetchQuery({
        queryKey: resultsKey(key),
        queryFn: () => api.experimentResults(key),
        staleTime: RESULTS_STALE,
      }),
    [qc],
  )
}

export function useMetrics() {
  return useQuery({ queryKey: ['metrics'], queryFn: api.metrics, staleTime: 5 * 60_000 })
}

function useInvalidateExperiments() {
  const qc = useQueryClient()
  return (key?: string) => {
    void qc.invalidateQueries({ queryKey: ['experiments'] })
    if (key) {
      void qc.invalidateQueries({ queryKey: ['experiment', key] })
      void qc.invalidateQueries({ queryKey: ['experimentResults', key] })
    }
  }
}

export function useSaveExperiment() {
  const invalidate = useInvalidateExperiments()
  return useMutation({
    mutationFn: ({ experiment, create, fileSha }: { experiment: Experiment; create: boolean; fileSha?: string }) =>
      create
        ? api.createExperiment(experiment)
        : api.updateExperiment(experiment.key, experiment, fileSha ?? ''),
    onSettled: (_r, _e, vars) => invalidate(vars.experiment.key),
  })
}

export function useSaveMetric() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ metric, create, fileSha }: { metric: Metric; create: boolean; fileSha?: string }) =>
      create ? api.createMetric(metric) : api.updateMetric(metric.key, metric, fileSha ?? ''),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['metrics'] })
    },
  })
}

export function usePower(req: PowerRequest | null) {
  return useQuery({
    queryKey: ['power', req],
    queryFn: () => api.power(req!),
    enabled: req !== null,
    staleTime: Infinity,
    placeholderData: (prev) => prev,
  })
}

// Reads results a hover already prefetched, without ever fetching.
export function useCachedResults(key: string | undefined) {
  return useQuery({
    queryKey: resultsKey(key ?? ''),
    queryFn: () => api.experimentResults(key!),
    enabled: false,
    staleTime: RESULTS_STALE,
  })
}
