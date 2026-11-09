import { launcherFetch } from "@/api/http"
import type { ContextUsage } from "@/store/chat"

export interface SessionSummary {
  id: string
  title: string
  preview: string
  message_count: number
  created: string
  updated: string
  pinned?: boolean
  archived?: boolean
}

export interface SessionSearchResult extends SessionSummary {
  snippet?: string
  message_id?: string
  message_role?: "user" | "assistant"
}

export interface SessionSource {
  title?: string
  url: string
  snippet?: string
}

export interface SessionBranchItem {
  id: string
  preview: string
  model_name?: string
  created_at?: string
}

export interface SessionBranchGroup {
  root_message_id: string
  session_key?: string
  active_version_id: string
  versions: SessionBranchItem[]
}

export interface SessionListPage {
  items: SessionSummary[]
  next_cursor?: string
  has_more: boolean
}

export interface SessionDetail {
  id: string
  messages: {
    message_id?: string
    role: "user" | "assistant"
    content: string
    created_at?: string
    kind?: "normal" | "thought" | "tool_calls"
    model_name?: string
    media?: string[]
    attachments?: {
      id?: string
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
    sources?: SessionSource[]
  }[]
  title?: string
  summary: string
  created: string
  updated: string
  next_cursor?: string
  has_more?: boolean
  turn?: SessionTurn
  turn_history?: SessionTurn[]
  feedback?: Record<string, "up" | "down">
  branches?: SessionBranchGroup[]
}

export interface SessionTurn {
  usage?: ContextUsage
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
  archived = false,
): Promise<SessionListPage> {
  const params = new URLSearchParams({ limit: limit.toString() })
  if (cursor) params.set("cursor", cursor)
  if (archived) params.set("archived", "1")

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

export async function searchSessions(
  query: string,
  archived = false,
  limit = 30,
  signal?: AbortSignal,
): Promise<SessionSearchResult[]> {
  const params = new URLSearchParams({ q: query, limit: limit.toString() })
  if (archived) params.set("archived", "1")
  const res = await launcherFetch(`/api/sessions/search?${params.toString()}`, {
    signal,
  })
  if (!res.ok) {
    throw new Error(`Failed to search sessions: ${res.status}`)
  }
  return res.json()
}

export async function updateSessionMetadata(
  id: string,
  update: { title?: string; pinned?: boolean; archived?: boolean },
): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(update),
  })
  if (!res.ok) {
    throw new Error(`Failed to update session ${id}: ${res.status}`)
  }
}

export async function setSessionFeedback(
  sessionId: string,
  messageId: string,
  value: "up" | "down" | "",
): Promise<void> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(sessionId)}/feedback/${encodeURIComponent(messageId)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ value }),
    },
  )
  if (!res.ok) {
    throw new Error(`Failed to save feedback: ${res.status}`)
  }
}

export interface UploadedAttachment {
  id: string
  type: "image" | "audio" | "video" | "file"
  url: string
  filename: string
  content_type?: string
}

export interface UploadConfig {
  max_media_size: number
}

export async function getUploadConfig(): Promise<UploadConfig> {
  const res = await launcherFetch("/api/uploads/config")
  if (!res.ok) {
    throw new Error(`Failed to fetch upload config: ${res.status}`)
  }
  return res.json()
}

export async function uploadSessionAttachment(
  sessionId: string,
  file: File,
): Promise<UploadedAttachment> {
  const body = new FormData()
  body.append("file", file, file.name)
  const res = await launcherFetch(
    `/api/uploads?session_id=${encodeURIComponent(sessionId)}`,
    { method: "POST", body },
  )
  if (!res.ok) {
    const detail = await res.text().catch(() => "")
    throw new Error(detail || `Upload failed: ${res.status}`)
  }
  return res.json()
}

export async function deleteSessionAttachment(
  sessionId: string,
  attachmentId: string,
): Promise<void> {
  const params = new URLSearchParams({
    session_id: sessionId,
    id: attachmentId,
  })
  const res = await launcherFetch(`/api/uploads?${params.toString()}`, {
    method: "DELETE",
  })
  if (!res.ok && res.status !== 404) {
    throw new Error(`Delete failed: ${res.status}`)
  }
}
