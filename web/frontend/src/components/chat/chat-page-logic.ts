import type { ChatInputDisabledReason } from "@/components/chat/chat-composer"
import type {
  ChatActivity,
  ChatMessage,
  ChatPlanStep,
  ChatTurnHistory,
  ConnectionState,
} from "@/store/chat"
import type { GatewayState } from "@/store/gateway"

export function resolveChatInputDisabledReason({
  hasDefaultModel,
  connectionState,
  gatewayState,
}: {
  hasDefaultModel: boolean
  connectionState: ConnectionState
  gatewayState: GatewayState
}): ChatInputDisabledReason | null {
  if (gatewayState === "unknown") {
    return "gatewayUnknown"
  }
  if (gatewayState === "starting") {
    return "gatewayStarting"
  }
  if (gatewayState === "restarting") {
    return "gatewayRestarting"
  }
  if (gatewayState === "stopping") {
    return "gatewayStopping"
  }
  if (gatewayState === "stopped") {
    return "gatewayStopped"
  }
  if (gatewayState === "error") {
    return "gatewayError"
  }
  if (connectionState === "connecting") {
    return "websocketConnecting"
  }
  if (connectionState === "error") {
    return "websocketError"
  }
  if (connectionState === "disconnected") {
    return "websocketDisconnected"
  }
  if (!hasDefaultModel) {
    return "noDefaultModel"
  }
  return null
}

export type ChatStreamRow =
  | { kind: "loadOlder" }
  | { kind: "historyLoading" }
  | { kind: "historyError" }
  | { kind: "empty" }
  | { kind: "message"; message: ChatMessage }
  | { kind: "process"; messages: ChatMessage[]; active?: boolean }
  | { kind: "working" }
  | {
      kind: "activity"
      plan?: ChatPlanStep[]
      activities?: ChatActivity[]
      history?: ChatTurnHistory[]
    }

export function chatStreamRowKey(row: ChatStreamRow): string {
  return row.kind === "message"
    ? `message:${row.message.id}`
    : row.kind === "process"
      ? `process:${row.messages[0].id}`
      : row.kind
}

export function estimateChatStreamRow(row: ChatStreamRow): number {
  switch (row.kind) {
    case "loadOlder":
      return 56
    case "historyLoading":
    case "historyError":
      return 104
    case "empty":
      return 420
    case "working":
      return 56
    case "activity":
      return 180
    case "process": {
      const count = row.messages.length
      const totalChars = row.messages.reduce(
        (acc, m) => acc + (m.content?.length || 0),
        0,
      )
      return Math.max(
        64,
        Math.min(600, 48 + count * 24 + Math.floor(totalChars / 8)),
      )
    }
    case "message": {
      const chars = row.message.content?.length || 0
      if (row.message.role === "user") {
        return Math.max(56, Math.min(300, 48 + Math.floor(chars / 5)))
      }
      return Math.max(80, Math.min(1200, 64 + Math.floor(chars / 3)))
    }
    default:
      return 112
  }
}
