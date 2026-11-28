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
const SPLIT_MARKER_PATTERN = /[ \t]*<\/?\|?\[?\s*SPLIT\s*\]?\|?>[ \t]*/gi
const SPLIT_MARKER_PREFIXES = ["<|[split]|>", "<|split|>", "<[split]>"]

function findMarkdownCodeRanges(content: string): Array<[number, number]> {
  const ranges: Array<[number, number]> = []
  const n = content.length
  let i = 0
  let lineStart = true

  while (i < n) {
    if (lineStart) {
      let indent = i
      while (indent < n && indent - i < 3 && content[indent] === " ") indent++
      const ch = content[indent]
      if (indent < n && (ch === "`" || ch === "~")) {
        let fenceLen = 0
        while (indent + fenceLen < n && content[indent + fenceLen] === ch) {
          fenceLen++
        }
        if (fenceLen >= 3) {
          let j = indent + fenceLen
          while (j < n && content[j] !== "\n") j++
          if (j < n) j++
          while (j < n) {
            const ls = j
            let ind = ls
            while (ind < n && ind - ls < 3 && content[ind] === " ") ind++
            let closeLen = 0
            while (ind + closeLen < n && content[ind + closeLen] === ch) {
              closeLen++
            }
            let lineEnd = ind + closeLen
            if (closeLen >= fenceLen) {
              let onlySpace = true
              while (lineEnd < n && content[lineEnd] !== "\n") {
                const c = content[lineEnd]
                if (c !== " " && c !== "\t" && c !== "\r") {
                  onlySpace = false
                  break
                }
                lineEnd++
              }
              if (onlySpace) {
                if (lineEnd < n && content[lineEnd] === "\n") lineEnd++
                j = lineEnd
                break
              }
            }
            while (j < n && content[j] !== "\n") j++
            if (j < n) j++
          }
          ranges.push([i, j])
          i = j
          lineStart = true
          continue
        }
      }
      lineStart = false
    }

    if (content[i] === "`") {
      let tickLen = 0
      while (i + tickLen < n && content[i + tickLen] === "`") tickLen++
      let j = i + tickLen
      let found = false
      while (j < n) {
        if (content[j] === "`") {
          let closeLen = 0
          while (j + closeLen < n && content[j + closeLen] === "`") closeLen++
          if (closeLen === tickLen) {
            ranges.push([i, j + closeLen])
            i = j + closeLen
            found = true
            break
          }
          j += closeLen
          continue
        }
        j++
      }
      if (found) continue
      i += tickLen
      continue
    }

    if (content[i] === "\n") lineStart = true
    i++
  }

  return ranges
}

function formatReplyTextContent(content: string): string {
  if (!content.includes("<")) return content

  const codeRanges = findMarkdownCodeRanges(content)
  let result = ""
  let cursor = 0

  const appendPlain = (segment: string) =>
    segment.replace(SPLIT_MARKER_PATTERN, "\n\n")

  for (const [start, end] of codeRanges) {
    if (start > cursor) result += appendPlain(content.slice(cursor, start))
    result += content.slice(start, end)
    cursor = end
  }
  if (cursor < content.length) {
    let tail = appendPlain(content.slice(cursor))
    const lastLT = tail.lastIndexOf("<")
    if (lastLT >= 0 && tail.length - lastLT <= 16) {
      const candidate = tail.slice(lastLT).toLowerCase()
      if (
        candidate.length >= 2 &&
        SPLIT_MARKER_PREFIXES.some(
          (p) => candidate.length < p.length && p.startsWith(candidate),
        )
      ) {
        tail = tail.slice(0, lastLT)
      }
    }
    result += tail
  }

  return result
}

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
      const formatted = formatReplyTextContent(message.content)
      const text =
        formatted !== message.content
          ? { ...message, content: formatted }
          : message
      reply.parts.push({ kind: "text", message: text })
      reply.lastText = message
      awaitingToolContinuation = false
    }
  }
  return rows
}
