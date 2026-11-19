import { launcherFetch } from "@/api/http"

export type ManagedSession = {
  id: string
  key: string
  count: number
  updated: string
  scope: {
    channel?: string
    agent_id?: string
    account?: string
    values?: Record<string, string>
  }
}

type ManagedMessage = {
  role: string
  content: string
  created_at?: string
  reasoning_content?: string
  media?: string[]
  attachments?: {
    type?: string
    url?: string
    ref?: string
    filename?: string
  }[]
  tool_calls?: {
    id?: string
    function?: { name?: string; arguments?: string }
  }[]
}

export type ManagedSessionPage = {
  items: ManagedSession[]
  next_cursor?: string
}

type ManagedTranscriptPage = {
  deliveries?: {
    id: string
    channel: string
    status: string
    updated_at: string
  }[]
  messages: ManagedMessage[] | null
  next_cursor: number
  has_more: boolean
}

const route = "/api/session-management"

async function request(url: string, init?: RequestInit): Promise<Response> {
  const response = await launcherFetch(url, init)
  if (!response.ok) throw new Error((await response.text()).trim())
  return response
}

export async function getManagedSessions(
  query: string,
  cursor: string,
  signal: AbortSignal,
): Promise<ManagedSessionPage> {
  const params = new URLSearchParams()
  if (query) params.set("q", query)
  if (cursor) params.set("cursor", cursor)
  const url = params.size ? `${route}?${params}` : route
  return (await request(url, { signal })).json()
}

export async function getManagedTranscript(
  id: string,
  before: number,
  signal: AbortSignal,
): Promise<ManagedTranscriptPage> {
  const path = `${route}/${encodeURIComponent(id)}`
  return (
    await request(before ? `${path}?before=${before}` : path, {
      signal,
    })
  ).json()
}

export async function deleteManagedSession(id: string): Promise<void> {
  const response = await launcherFetch(`${route}/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
  if (response.status === 409) throw new SessionRuntimeActiveError()
  if (!response.ok) throw new Error((await response.text()).trim())
}

export class SessionRuntimeActiveError extends Error {}
