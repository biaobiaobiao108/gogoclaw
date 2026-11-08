import { getSessionHistory } from "@/api/sessions"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  parseToolCallsValue,
  toolCallsSignature,
} from "@/features/chat/tool-calls"
import type {
  ChatActivity,
  ChatAttachment,
  ChatMessage,
  ChatPlanStep,
  ChatTurnHistory,
} from "@/store/chat"

function toChatAttachments({
  media,
  attachments,
}: {
  media?: string[]
  attachments?: {
    type?: "image" | "audio" | "video" | "file"
    url: string
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
          filename: attachment.filename,
          contentType: attachment.content_type,
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
}> {
  const detail = await getSessionHistory(sessionId)
  const messages = detail.messages.map((message, index) => ({
    id: `hist-${index}-${Date.now()}`,
    role: message.role,
    content: message.content,
    kind: message.role === "assistant" ? (message.kind ?? "normal") : undefined,
    modelName: message.model_name,
    toolCalls:
      message.role === "assistant"
        ? parseToolCallsValue(message.tool_calls)
        : undefined,
    attachments: toChatAttachments({
      media: message.media,
      attachments: message.attachments,
    }),
    timestamp: message.created_at ?? detail.updated,
  }))

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
  }
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
    ...currentMessages,
  ]

  return merged.sort(
    (left, right) =>
      comparableTimestamp(left.timestamp) -
      comparableTimestamp(right.timestamp),
  )
}
