import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  api,
  type Flag,
  type FlagList,
  type NewVariation,
  type Outcome,
  type RolloutStep,
} from '@/lib/api'

export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: api.me, retry: false })
}

export function useFlags(env: string | undefined) {
  return useQuery({
    queryKey: ['flags', env],
    queryFn: () => api.flags(env!),
    enabled: Boolean(env),
  })
}

export function useFlag(env: string | undefined, key: string | undefined) {
  return useQuery({
    queryKey: ['flag', env, key],
    queryFn: () => api.flag(env!, key!),
    enabled: Boolean(env && key),
  })
}

export function useSetState(env: string) {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: ({ flag, enabled }: { flag: Flag; enabled: boolean }) =>
      api.setState(env, flag.key, enabled, flag.fileSha),

    onMutate: async ({ flag, enabled }) => {
      await qc.cancelQueries({ queryKey: ['flags', env] })
      const previous = qc.getQueryData<FlagList>(['flags', env])

      qc.setQueryData<FlagList>(['flags', env], (old) =>
        old
          ? {
              ...old,
              flags: old.flags.map((f) => (f.key === flag.key ? { ...f, enabled } : f)),
            }
          : old,
      )

      return { previous }
    },

    onError: (_err, _vars, context) => {
      if (context?.previous) qc.setQueryData(['flags', env], context.previous)
      void qc.invalidateQueries({ queryKey: ['flags', env] })
    },

    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['flags', env] })
      void qc.invalidateQueries({ queryKey: ['flag', env] })
    },
  })
}

export function useSetRollout(env: string) {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: ({
      flag,
      ruleName,
      percentage,
    }: {
      flag: Flag
      ruleName: string
      percentage: Record<string, number>
    }) => api.setRollout(env, flag.key, ruleName, percentage, flag.fileSha),

    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['flags', env] })
      void qc.invalidateQueries({ queryKey: ['flag', env] })
    },
  })
}

export function useAttributes(env: string | undefined) {
  return useQuery({
    queryKey: ['attributes', env],
    queryFn: () => api.attributes(env!),
    enabled: Boolean(env),
    staleTime: 60_000,
  })
}

export function useSaveRule(env: string) {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: ({
      flag,
      ruleName,
      query,
    }: {
      flag: Flag
      ruleName: string
      query: string
    }) => api.saveRule(env, flag.key, ruleName, query, flag.fileSha),

    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['flags', env] })
      void qc.invalidateQueries({ queryKey: ['flag', env] })
      void qc.invalidateQueries({ queryKey: ['attributes', env] })
    },
  })
}

export function useHistory(
  env: string | undefined,
  key: string | undefined,
  available = true,
) {
  return useQuery({
    queryKey: ['history', env, key],
    queryFn: () => api.history(env!, key!),
    enabled: available && Boolean(env && key),
  })
}

function invalidateFlag(qc: ReturnType<typeof useQueryClient>, env: string) {
  void qc.invalidateQueries({ queryKey: ['flags', env] })
  void qc.invalidateQueries({ queryKey: ['flag', env] })
  void qc.invalidateQueries({ queryKey: ['attributes', env] })
}

export function useCreateFlag(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload: {
      key: string
      team: string
      type: string
      variations: NewVariation[]
      default: string
      enabled: boolean
    }) => api.createFlag(env, payload),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useCreateTeam(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.createTeam(env, name),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useDeleteFlag(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (flag: Flag) => api.deleteFlag(env, flag.key, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useSetProgressive(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      flag,
      ruleName,
      initial,
      end,
      clear,
      variation,
    }: {
      flag: Flag
      ruleName: string
      initial?: RolloutStep
      end?: RolloutStep
      clear?: boolean
      variation?: string
    }) =>
      api.setProgressive(env, flag.key, {
        ruleName,
        initial,
        end,
        clear,
        variation,
        fileSha: flag.fileSha,
      }),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useSetExperimentation(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      flag,
      start,
      end,
      clear,
    }: {
      flag: Flag
      start?: string
      end?: string
      clear?: boolean
    }) => api.setExperimentation(env, flag.key, { start, end, clear, fileSha: flag.fileSha }),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useRenameFlag(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ flag, newKey }: { flag: Flag; newKey: string }) =>
      api.renameFlag(env, flag.key, newKey, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useSetVariations(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      flag,
      variations,
      defaultVariation,
    }: {
      flag: Flag
      variations: NewVariation[]
      defaultVariation: string
    }) => api.setVariations(env, flag.key, variations, defaultVariation, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useAddRule(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      flag,
      name,
      query,
      outcome,
      position,
    }: {
      flag: Flag
      name: string
      query: string
      outcome: Outcome
      position?: number
    }) => api.addRule(env, flag.key, { name, query, outcome, position }, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useDeleteRule(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ flag, ruleName }: { flag: Flag; ruleName: string }) =>
      api.deleteRule(env, flag.key, ruleName, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useReorderRules(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ flag, order }: { flag: Flag; order: string[] }) =>
      api.reorderRules(env, flag.key, order, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useEditRule(env: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      flag,
      ruleName,
      query,
      outcome,
      disabled,
    }: {
      flag: Flag
      ruleName: string
      query?: string
      outcome?: Outcome
      disabled?: boolean
    }) => api.editRule(env, flag.key, { ruleName, query, outcome, disabled }, flag.fileSha),
    onSettled: () => invalidateFlag(qc, env),
  })
}

export function useCreateEnvironment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, file }: { name: string; file?: string }) =>
      api.createEnvironment(name, file),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['me'] })
    },
  })
}
