import { getSessionHistory } from "@/api/sessions"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  parseToolCallsValue,
  toolCallsSignature,
} from "@/features/chat/tool-calls"
import type {
  ChatActivity,
  ChatAttachment,
  ChatBranchGroup,
  ChatMessage,
  ChatPlanStep,
  ChatTurnHistory,
  ContextUsage,
} from "@/store/chat"

function toChatAttachments({
  media,
  attachments,
}: {
  media?: string[]
  attachments?: {
    type?: "image" | "audio" | "video" | "file"
    url: string
    id?: string
    filename?: string
    content_type?: string
  }[]
}): ChatAttachment[] | undefined {
  const normalizedAttachments = attachments
    ?.filter((attachment) => attachment.url)
    .map(
      (attachment) =>
        ({
          type: attachment.type ?? "file",
          url: attachment.url,
          id: attachment.id,
          filename: attachment.filename,
          contentType: attachment.content_type,
          persisted: true,
        }) satisfies ChatAttachment,
    )

  const legacyMediaAttachments = (media ?? [])
    .filter((item) => item.startsWith("data:image/"))
    .map((url) => ({ type: "image" as const, url }))

  const merged = [...(normalizedAttachments ?? []), ...legacyMediaAttachments]

  return merged.length > 0 ? merged : undefined
}

export async function loadSessionMessages(
  sessionId: string,
): Promise<ChatMessage[]> {
  return (await loadSessionHistory(sessionId)).messages
}

export async function loadSessionHistory(sessionId: string): Promise<{
  messages: ChatMessage[]
  activities: ChatActivity[]
  plan: ChatPlanStep[]
  turnHistory: ChatTurnHistory[]
  contextUsage?: ContextUsage
  nextCursor?: string
  hasOlder: boolean
  branches?: ChatBranchGroup[]
}> {
  const detail = await getSessionHistory(sessionId, { limit: 80 })
  const messages = detail.messages.map((message, index) => {
    const id = historyMessageId(message, index)
    return {
      id,
      role: message.role,
      content: message.content,
      kind:
        message.role === "assistant" ? (message.kind ?? "normal") : undefined,
      modelName: message.model_name,
      toolCalls:
        message.role === "assistant"
          ? parseToolCallsValue(message.tool_calls)
          : undefined,
      attachments: toChatAttachments({
        media: message.media,
        attachments: message.attachments,
      }),
      sources: message.sources,
      feedback: detail.feedback?.[id],
      timestamp: message.created_at ?? detail.updated,
    }
  })

  const toActivities = (
    activities:
      | {
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
      | undefined,
  ) =>
    (activities ?? []).map((activity) => ({
      id: activity.id,
      kind: activity.kind,
      status: activity.status,
      title: activity.title,
      toolCallId: activity.tool_call_id,
      tool: activity.tool,
      detail: activity.detail,
      output: activity.output,
      durationMs: activity.duration_ms,
      exitCode: activity.exit_code,
      diff: activity.diff,
      artifacts: activity.artifacts,
      startedAt: activity.started_at,
      completedAt: activity.completed_at,
    }))

  const turn = detail.turn
  return {
    messages,
    contextUsage: turn?.usage,
    plan: turn?.plan ?? [],
    activities: toActivities(turn?.activities),
    turnHistory: (detail.turn_history ?? [])
      .filter((item) => item.turn_id)
      .map((item) => ({
        turnId: item.turn_id,
        status: item.status,
        iteration: item.iteration,
        plan: item.plan ?? [],
        activities: toActivities(item.activities),
      })),
    nextCursor: detail.next_cursor,
    hasOlder: detail.has_more === true,
    branches: (detail.branches ?? []).map((group) => ({
      rootMessageId: group.root_message_id,
      sessionKey: group.session_key,
      activeVersionId: group.active_version_id,
      versions: group.versions.map((version) => ({
        id: version.id,
        preview: version.preview,
        modelName: version.model_name,
        createdAt: version.created_at,
      })),
    })),
  }
}

export async function loadOlderSessionHistory(
  sessionId: string,
  cursor: string,
): Promise<{
  messages: ChatMessage[]
  nextCursor?: string
  hasOlder: boolean
}> {
  const detail = await getSessionHistory(sessionId, { cursor, limit: 60 })
  return {
    messages: detail.messages.map((message, index) => {
      const id = historyMessageId(message, index, cursor)
      return {
        id,
        role: message.role,
        content: message.content,
        kind:
          message.role === "assistant" ? (message.kind ?? "normal") : undefined,
        modelName: message.model_name,
        toolCalls:
          message.role === "assistant"
            ? parseToolCallsValue(message.tool_calls)
            : undefined,
        attachments: toChatAttachments({
          media: message.media,
          attachments: message.attachments,
        }),
        sources: message.sources,
        feedback: detail.feedback?.[id],
        timestamp: message.created_at ?? detail.updated,
      }
    }),
    nextCursor: detail.next_cursor,
    hasOlder: detail.has_more === true,
  }
}

function historyMessageId(
  message: {
    message_id?: string
    role: "user" | "assistant"
    content: string
    created_at?: string
    kind?: "normal" | "thought" | "tool_calls"
  },
  index: number,
  page?: string,
): string {
  if (message.message_id) return message.message_id
  let hash = 2166136261
  const value = `${message.role}\u0000${message.created_at ?? ""}\u0000${message.kind ?? ""}\u0000${message.content}`
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return `hist-${page ?? "latest"}-${index}-${(hash >>> 0).toString(36)}`
}

function normalizeMessageTimestamp(timestamp: number | string): string {
  if (typeof timestamp === "number") {
    return String(normalizeUnixTimestamp(timestamp))
  }

  const trimmed = timestamp.trim()
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
    return String(normalizeUnixTimestamp(Number(trimmed)))
  }

  const parsed = Date.parse(trimmed)
  return Number.isNaN(parsed) ? trimmed : String(parsed)
}

function messageSignature(message: ChatMessage): string {
  const attachmentSignature = (message.attachments ?? [])
    .map(
      (attachment) =>
        `${attachment.type}\u0001${attachment.url}\u0001${attachment.filename ?? ""}`,
    )
    .join("\u0002")

  return `${message.role}\u0000${message.content}\u0000${normalizeMessageTimestamp(
    message.timestamp,
  )}\u0000${message.kind ?? ""}\u0000${message.modelName ?? ""}\u0000${attachmentSignature}\u0000${toolCallsSignature(
    message.toolCalls,
  )}`
}

function comparableTimestamp(timestamp: number | string): number {
  const normalized = normalizeMessageTimestamp(timestamp)
  const numeric = Number(normalized)
  return Number.isFinite(numeric) ? numeric : 0
}

export function mergeHistoryMessages(
  historyMessages: ChatMessage[],
  currentMessages: ChatMessage[],
): ChatMessage[] {
  const savedById = new Map(
    historyMessages.map((message) => [message.id, message]),
  )
  const currentIds = new Set(currentMessages.map((message) => message.id))
  const currentSignatures = new Set(
    currentMessages.map((message) => messageSignature(message)),
  )

  const merged = [
    ...historyMessages.filter(
      (message) =>
        !currentIds.has(message.id) &&
        !currentSignatures.has(messageSignature(message)),
    ),
    ...currentMessages.map((message) => {
      const saved = savedById.get(message.id)
      return saved
        ? {
            ...message,
            ...saved,
            timestamp: saved.timestamp,
            ...(message.deliveryStatus
              ? { deliveryStatus: "accepted" as const }
              : {}),
          }
        : message
    }),
  ]

  return merged.sort(
    (left, right) =>
      comparableTimestamp(left.timestamp) -
      comparableTimestamp(right.timestamp),
  )
}
