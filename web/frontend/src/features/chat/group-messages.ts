import type { ChatMessage, ChatTurnHistory } from "@/store/chat"

export type ChatReplyPart =
  | { kind: "text"; message: ChatMessage }
  | { kind: "steering"; message: ChatMessage }
  | { kind: "process"; messages: ChatMessage[] }

export interface ChatReplyRow {
  kind: "reply"
  id: string
  parts: ChatReplyPart[]
  lastText?: ChatMessage
  active?: boolean
  execution?: ChatTurnHistory
}

type ChatMessageRow = { kind: "message"; message: ChatMessage } | ChatReplyRow

// Live Pico frames and saved transcript entries share the iteration ID.
const TURN_MESSAGE =
  /^(.+-turn-[\da-f]{8}(?:-[\da-f]{4}){3}-[\da-f]{12})-llm-\d+(?:-thought|-tools)?$/i
const LEGACY_STREAM_MESSAGE =
  /^[\da-f]{8}(?:-[\da-f]{4}){3}-[\da-f]{12}(?:-thought|-tools)?$/i
const MESSAGE_SPLIT_MARKER = "<|[SPLIT]|>"

export function groupChatMessages(messages: ChatMessage[]): ChatMessageRow[] {
  const rows: ChatMessageRow[] = []
  let reply: ChatReplyRow | undefined
  let replyTurnId: string | undefined
  let awaitingToolContinuation = false

  for (const message of messages) {
    if (message.role === "user") {
      if (message.turnId) {
        if (!reply || replyTurnId !== message.turnId) {
          reply = { kind: "reply", id: message.turnId, parts: [] }
          replyTurnId = message.turnId
          awaitingToolContinuation = false
          rows.push(reply)
        }
        reply.parts.push({ kind: "steering", message })
        continue
      }
      rows.push({ kind: "message", message })
      reply = undefined
      replyTurnId = undefined
      awaitingToolContinuation = false
      continue
    }

    const turnId = message.turnId || TURN_MESSAGE.exec(message.id)?.[1]
    const isThought = message.kind === "thought"
    const isToolCalls = message.kind === "tool_calls"
    const stepId = message.id.replace(/-(?:thought|tools)$/, "")
    const previous = reply?.parts.at(-1)
    const previousProcessMessage =
      previous?.kind === "process" ? previous.messages.at(-1) : undefined
    const sameStep =
      (isToolCalls && reply?.lastText?.id === stepId) ||
      previousProcessMessage?.id === `${stepId}-thought`
    // Older stream IDs were UUIDs per iteration. Their linked tool-call record
    // is the boundary that allows the next iteration to continue the reply.
    const legacyContinuation =
      awaitingToolContinuation &&
      (LEGACY_STREAM_MESSAGE.test(message.id) || (!replyTurnId && turnId))

    const sameTurn = turnId !== undefined && turnId === replyTurnId
    const sameLegacyProcess =
      !turnId &&
      !replyTurnId &&
      previous?.kind === "process" &&
      (isThought || isToolCalls)
    if (
      !reply ||
      (!sameTurn && !sameStep && !legacyContinuation && !sameLegacyProcess)
    ) {
      reply = { kind: "reply", id: turnId ?? stepId, parts: [] }
      rows.push(reply)
      replyTurnId = turnId
      awaitingToolContinuation = false
    }
    replyTurnId ??= turnId

    if (isThought || isToolCalls) {
      const last = reply.parts.at(-1)
      if (last?.kind === "process") last.messages.push(message)
      else reply.parts.push({ kind: "process", messages: [message] })
      if (isToolCalls && (turnId || LEGACY_STREAM_MESSAGE.test(message.id))) {
        awaitingToolContinuation = true
      }
    } else {
      const text = message.content.includes(MESSAGE_SPLIT_MARKER)
        ? {
            ...message,
            content: message.content.replaceAll(MESSAGE_SPLIT_MARKER, "\n\n"),
          }
        : message
      reply.parts.push({ kind: "text", message: text })
      reply.lastText = message
      awaitingToolContinuation = false
    }
  }
  return rows
}
