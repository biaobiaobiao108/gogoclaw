import type { ChatMessage } from "@/store/chat"

export function groupChatMessages(messages: ChatMessage[]) {
  const rows: (
    | { kind: "message"; message: ChatMessage }
    | { kind: "process"; messages: ChatMessage[] }
  )[] = []
  for (const message of messages) {
    if (
      message.role === "assistant" &&
      (message.kind === "thought" || message.kind === "tool_calls")
    ) {
      const previous = rows.at(-1)
      if (previous?.kind === "process") previous.messages.push(message)
      else rows.push({ kind: "process", messages: [message] })
    } else rows.push({ kind: "message", message })
  }
  return rows
}
