import { launcherFetch } from "@/api/http"

export interface EvolutionDraft {
  id: string
  target_skill_name: string
  human_summary: string
  status: string
  published_version?: string
  scan_findings?: string[]
}

export interface EvolutionOverview {
  mode: "" | "observe" | "draft" | "apply"
  drafts: EvolutionDraft[]
  archive_suggestions: string[]
  run: { running: boolean; last_run?: string; last_error?: string }
}

export interface EvolutionReview {
  draft: EvolutionDraft
  diff: string
  issue?: string
  can_refresh?: boolean
  can_rollback?: boolean
  shadowed_source?: string
  review_token?: string
  evidence: { summary: string; success: boolean | null }[]
}

export type EvolutionAction =
  | { action: "run" }
  | { action: "reject" | "rollback" | "refresh"; draft_id: string }
  | { action: "publish"; draft_id: string; review_token: string }

async function request<T>(
  path = "",
  action?: EvolutionAction,
  signal?: AbortSignal,
) {
  const res = await launcherFetch("/api/evolution" + path, {
    method: action ? "POST" : "GET",
    headers: action ? { "Content-Type": "application/json" } : undefined,
    body: action ? JSON.stringify(action) : undefined,
    signal,
  })
  if (!res.ok) {
    const detail = await res.json().catch(() => null)
    throw new Error(detail?.error ?? String(res.status))
  }
  return res.json() as Promise<T>
}

export function getEvolution(signal: AbortSignal) {
  return request<EvolutionOverview>("", undefined, signal)
}

export function getEvolutionReview(id: string, signal: AbortSignal) {
  return request<EvolutionReview>(
    `?draft=${encodeURIComponent(id)}`,
    undefined,
    signal,
  )
}

export function updateEvolution(action: EvolutionAction) {
  return request<{ ok: true }>("", action)
}
