export type Action =
  | 'view'
  | 'toggle'
  | 'rollout'
  | 'edit_rules'
  | 'edit_variations'
  | 'create'
  | 'delete'

export interface Environment {
  name: string
  display: string
  protected: boolean
  order: number
}

export interface Capabilities {
  history: boolean
  attribution: boolean
  review: boolean
}

export interface Me {
  name: string
  email: string
  groups: string[]
  environments: Environment[]
  pollSeconds: number
  capabilities: Capabilities
}

export interface Variation {
  name: string
  value: unknown
}

export interface Outcome {
  variation?: string
  percentage?: Record<string, number>
}

export interface Condition {
  op?: 'and' | 'or'
  not?: boolean
  children?: Condition[]
  attribute?: string
  operator?: string
  value?: string
}

export interface RolloutStep {
  variation: string
  percentage: number
  date: string
}

export interface ProgressiveRollout {
  initial: RolloutStep
  end: RolloutStep
}

export interface Experimentation {
  start?: string
  end?: string
}

export interface Rule {
  name: string
  query: string
  condition?: Condition
  advanced: boolean
  disabled?: boolean
  outcome: Outcome
  progressive?: ProgressiveRollout
}

export interface Flag {
  key: string
  file: string
  environment: string
  type: 'boolean' | 'string' | 'number' | 'json'
  enabled: boolean
  variations: Variation[] | null
  rules: Rule[] | null
  default: Outcome
  experimentation?: Experimentation
  metadata?: Record<string, unknown>
  team: string
  preserved?: string[]
  actions: Action[]
  summary: string
  fileSha: string
}

export interface Broken {
  key: string
  file: string
  reason: string
}

export interface TeamOption {
  name: string
  file: string
}

export interface FlagList {
  flags: Flag[]
  broken: Broken[] | null
  teams: TeamOption[] | null
}

export interface SaveResult {
  commit: string
  retried: boolean
  message: string
}

export interface EvalResult {
  variation: string
  value: unknown
  reason: string
  error?: string
}

export interface Commit {
  sha: string
  message: string
  author: string
  email: string
  when: string
}

export interface NewVariation {
  name: string
  value: string
}

export interface DiffResult {
  description: string
  diff: string
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
  get isConflict() {
    return this.status === 409
  }
  get isUnauthenticated() {
    return this.status === 401
  }
  get isForbidden() {
    return this.status === 403
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
  })

  if (!res.ok) {
    let message = `Something went wrong (${res.status})`
    try {
      const body = await res.json()
      if (body?.error) message = body.error
    } catch {
      // keep the generic message
    }
    throw new ApiError(res.status, message)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  me: () => request<Me>('/api/me'),

  flags: (env: string) => request<FlagList>(`/api/environments/${env}/flags`),

  flag: (env: string, key: string) =>
    request<Flag>(`/api/environments/${env}/flags/${encodeURIComponent(key)}`),

  setState: (env: string, key: string, enabled: boolean, fileSha: string) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/state`, {
      method: 'POST',
      body: JSON.stringify({ enabled, fileSha }),
    }),

  setRollout: (
    env: string,
    key: string,
    ruleName: string,
    percentage: Record<string, number>,
    fileSha: string,
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/rollout`, {
      method: 'POST',
      body: JSON.stringify({ ruleName, percentage, fileSha }),
    }),

  preview: (env: string, key: string, targetingKey: string, attributes: Record<string, unknown>) =>
    request<EvalResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/preview`, {
      method: 'POST',
      body: JSON.stringify({ targetingKey, attributes }),
    }),

  history: (env: string, key: string) =>
    request<Commit[] | null>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/history`),

  diffState: (env: string, key: string, enabled: boolean) =>
    request<DiffResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/diff`, {
      method: 'POST',
      body: JSON.stringify({ change: 'state', enabled }),
    }),

  diffRollout: (env: string, key: string, ruleName: string, percentage: Record<string, number>) =>
    request<DiffResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/diff`, {
      method: 'POST',
      body: JSON.stringify({ change: 'rollout', ruleName, percentage }),
    }),

  attributes: (env: string) => request<string[]>(`/api/environments/${env}/attributes`),

  saveRule: (
    env: string,
    key: string,
    ruleName: string,
    query: string,
    fileSha: string,
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/rule`, {
      method: 'POST',
      body: JSON.stringify({ ruleName, query, fileSha }),
    }),

  diffRule: (env: string, key: string, ruleName: string, query: string) =>
    request<DiffResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/diff`, {
      method: 'POST',
      body: JSON.stringify({ change: 'rule', ruleName, query }),
    }),

  createFlag: (
    env: string,
    payload: {
      key: string
      team: string
      type: string
      variations: NewVariation[]
      default: string
      enabled: boolean
    },
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags`, {
      method: 'POST',
      body: JSON.stringify(payload),
    }),

  createTeam: (env: string, name: string) =>
    request<{ name: string }>(`/api/environments/${env}/teams`, {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  deleteFlag: (env: string, key: string, fileSha: string) =>
    request<SaveResult>(
      `/api/environments/${env}/flags/${encodeURIComponent(key)}?fileSha=${encodeURIComponent(fileSha)}`,
      { method: 'DELETE' },
    ),

  setProgressive: (
    env: string,
    key: string,
    payload: {
      ruleName: string
      initial?: RolloutStep
      end?: RolloutStep
      clear?: boolean
      variation?: string
      fileSha: string
    },
  ) =>
    request<SaveResult>(
      `/api/environments/${env}/flags/${encodeURIComponent(key)}/progressive`,
      { method: 'POST', body: JSON.stringify(payload) },
    ),

  setExperimentation: (
    env: string,
    key: string,
    payload: { start?: string; end?: string; clear?: boolean; fileSha: string },
  ) =>
    request<SaveResult>(
      `/api/environments/${env}/flags/${encodeURIComponent(key)}/experimentation`,
      { method: 'POST', body: JSON.stringify(payload) },
    ),

  renameFlag: (env: string, key: string, newKey: string, fileSha: string) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/key`, {
      method: 'POST',
      body: JSON.stringify({ key: newKey, fileSha }),
    }),

  setVariations: (
    env: string,
    key: string,
    variations: NewVariation[],
    defaultVariation: string,
    fileSha: string,
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/variations`, {
      method: 'PUT',
      body: JSON.stringify({ variations, default: defaultVariation, fileSha }),
    }),

  addRule: (
    env: string,
    key: string,
    payload: { name: string; query: string; outcome: Outcome; position?: number },
    fileSha: string,
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/rules`, {
      method: 'POST',
      body: JSON.stringify({ ...payload, fileSha }),
    }),

  deleteRule: (env: string, key: string, ruleName: string, fileSha: string) =>
    request<SaveResult>(
      `/api/environments/${env}/flags/${encodeURIComponent(key)}/rules/${encodeURIComponent(ruleName)}?fileSha=${encodeURIComponent(fileSha)}`,
      { method: 'DELETE' },
    ),

  reorderRules: (env: string, key: string, order: string[], fileSha: string) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/rules/order`, {
      method: 'PUT',
      body: JSON.stringify({ order, fileSha }),
    }),

  editRule: (
    env: string,
    key: string,
    payload: { ruleName: string; query?: string; outcome?: Outcome; disabled?: boolean },
    fileSha: string,
  ) =>
    request<SaveResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/rule`, {
      method: 'POST',
      body: JSON.stringify({ ...payload, fileSha }),
    }),

  diffGeneric: (env: string, key: string, body: Record<string, unknown>) =>
    request<DiffResult>(`/api/environments/${env}/flags/${encodeURIComponent(key)}/diff`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  createEnvironment: (name: string, file?: string) =>
    request<{ name: string }>('/api/environments', {
      method: 'POST',
      body: JSON.stringify({ name, file }),
    }),

  logout: () => request<void>('/auth/logout', { method: 'POST' }),
}
