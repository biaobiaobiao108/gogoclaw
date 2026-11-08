import { launcherFetch } from "@/api/http"

export interface SessionSummary {
  id: string
  title: string
  preview: string
  message_count: number
  created: string
  updated: string
}

export interface SessionListPage {
  items: SessionSummary[]
  next_cursor?: string
  has_more: boolean
}

export interface SessionDetail {
  id: string
  messages: {
    role: "user" | "assistant"
    content: string
    created_at?: string
    kind?: "normal" | "thought" | "tool_calls"
    model_name?: string
    media?: string[]
    attachments?: {
      type?: "image" | "audio" | "video" | "file"
      url: string
      filename?: string
      content_type?: string
    }[]
    tool_calls?: {
      id?: string
      type?: string
      function?: {
        name?: string
        arguments?: string
      }
      extra_content?: {
        tool_feedback_explanation?: string
      }
    }[]
  }[]
  summary: string
  created: string
  updated: string
  next_cursor?: string
  has_more?: boolean
  turn?: SessionTurn
  turn_history?: SessionTurn[]
}

export interface SessionTurn {
  turn_id: string
  status: string
  iteration: number
  plan?: { text: string; status: string }[]
  activities?: {
    id: string
    kind: string
    status: string
    title: string
    tool_call_id?: string
    tool?: string
    detail?: string
    output?: string
    duration_ms?: number
    exit_code?: number
    diff?: string
    artifacts?: string[]
    started_at: string
    completed_at?: string
  }[]
  delivery?: { status?: string }
}

export async function getSessions(
  cursor?: string,
  limit: number = 30,
): Promise<SessionListPage> {
  const params = new URLSearchParams({ limit: limit.toString() })
  if (cursor) params.set("cursor", cursor)

  const res = await launcherFetch(`/api/sessions?${params.toString()}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.status}`)
  }
  return res.json()
}

export async function getSessionHistory(
  id: string,
  options?: { cursor?: string; limit?: number },
): Promise<SessionDetail> {
  const params = new URLSearchParams()
  if (options?.cursor) params.set("cursor", options.cursor)
  if (options?.limit) params.set("limit", options.limit.toString())
  const query = params.size > 0 ? `?${params.toString()}` : ""
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}${query}`,
  )
  if (!res.ok) {
    throw new Error(`Failed to fetch session ${id}: ${res.status}`)
  }
  return res.json()
}

export async function deleteSession(id: string): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
  if (!res.ok) {
    throw new Error(`Failed to delete session ${id}: ${res.status}`)
  }
}
