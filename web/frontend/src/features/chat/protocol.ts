import { toast } from "sonner"

import {
  parseAssistantMessageCreateState,
  parseAssistantMessageUpdateState,
} from "@/features/chat/assistant-message-state"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import i18n from "@/i18n"
import {
  type ChatApproval,
  type ChatAttachment,
  type ContextUsage,
  updateChatStore,
} from "@/store/chat"

export interface PicoMessage {
  type: string
  id?: string
  session_id?: string
  timestamp?: number | string
  payload?: Record<string, unknown>
}

let lastTurnErrorSignature = ""

function parseAttachments(
  payload: Record<string, unknown>,
): ChatAttachment[] | undefined {
  const raw = payload.attachments
  if (!Array.isArray(raw)) {
    return undefined
  }

  const attachments: ChatAttachment[] = []
  for (const item of raw) {
    if (!item || typeof item !== "object") {
      continue
    }

    const attachment = item as Record<string, unknown>
    const url = typeof attachment.url === "string" ? attachment.url : ""
    if (!url) {
      continue
    }

    const type =
      attachment.type === "audio" ||
      attachment.type === "video" ||
      attachment.type === "file" ||
      attachment.type === "image"
        ? attachment.type
        : "file"

    const filename =
      typeof attachment.filename === "string" ? attachment.filename : undefined
    const contentType =
      typeof attachment.content_type === "string"
        ? attachment.content_type
        : undefined

    attachments.push({
      type,
      url,
      ...(filename ? { filename } : {}),
      ...(contentType ? { contentType } : {}),
    })
  }

  return attachments.length > 0 ? attachments : undefined
}

function parseContextUsage(
  payload: Record<string, unknown>,
): ContextUsage | undefined {
  const raw = payload.context_usage
  if (!raw || typeof raw !== "object") return undefined
  const obj = raw as Record<string, unknown>
  const used = Number(obj.used_tokens)
  const total = Number(obj.total_tokens)
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0)
    return undefined
  return {
    used_tokens: used,
    total_tokens: total,
    history_tokens:
      obj.history_tokens != null ? Number(obj.history_tokens) : undefined,
    compress_at_tokens: Number(obj.compress_at_tokens) || 0,
    summarize_at_tokens:
      obj.summarize_at_tokens != null
        ? Number(obj.summarize_at_tokens)
        : undefined,
    used_percent: Number(obj.used_percent) || 0,
  }
}

function parseModelName(payload: Record<string, unknown>): string | undefined {
  if (typeof payload.model_name !== "string") {
    return undefined
  }
  const modelName = payload.model_name.trim()
  return modelName || undefined
}

function parseApprovalTime(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value
  if (typeof value !== "string") return undefined
  const numeric = Number(value)
  if (Number.isFinite(numeric) && numeric > 0) return numeric
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function parseApproval(raw: unknown): ChatApproval | undefined {
  if (!raw || typeof raw !== "object") return undefined
  const value = raw as Record<string, unknown>
  const id =
    typeof value.approval_id === "string"
      ? value.approval_id
      : typeof value.id === "string"
        ? value.id
        : ""
  const tool = typeof value.tool === "string" ? value.tool : ""
  if (!id || !tool) return undefined
  return {
    id,
    tool,
    arguments:
      value.arguments && typeof value.arguments === "object"
        ? (value.arguments as Record<string, unknown>)
        : undefined,
    status: typeof value.status === "string" ? value.status : "pending",
    risk:
      typeof value.risk === "string"
        ? value.risk
        : typeof value.risk_summary === "string"
          ? value.risk_summary
          : undefined,
    createdAt: parseApprovalTime(value.created_at),
    expiresAt: parseApprovalTime(value.expires_at),
  }
}

export function handlePicoMessage(
  message: PicoMessage,
  expectedSessionId: string,
): string | undefined {
  if (message.session_id && message.session_id !== expectedSessionId) {
    return
  }

  const payload = message.payload || {}

  switch (message.type) {
    case "turn.status": {
      const turnError =
        typeof payload.error === "string" ? payload.error.trim() : ""
      const errorSignature = turnError
        ? `${expectedSessionId}\u0000${turnError}`
        : ""
      if (turnError && errorSignature !== lastTurnErrorSignature) {
        toast.error(turnError)
        lastTurnErrorSignature = errorSignature
      } else if (
        !turnError &&
        lastTurnErrorSignature.startsWith(`${expectedSessionId}\u0000`)
      ) {
        lastTurnErrorSignature = ""
      }
      const active = payload.active === true
      const approval = parseApproval(payload.approval)
      const plan = Array.isArray(payload.plan)
        ? payload.plan.filter(
            (step): step is { text: string; status: string } =>
              !!step &&
              typeof step === "object" &&
              typeof step.text === "string" &&
              typeof step.status === "string",
          )
        : undefined
      updateChatStore({
        activeTurn: active
          ? {
              turnId:
                typeof payload.turn_id === "string" ? payload.turn_id : "",
              phase: typeof payload.phase === "string" ? payload.phase : "",
              iteration: Number(payload.iteration) || 0,
              plan,
            }
          : undefined,
        ...(plan ? { recentPlan: plan } : {}),
        ...(typeof payload.interrupted === "boolean"
          ? { interrupted: payload.interrupted }
          : active
            ? { interrupted: false }
            : {}),
        ...(approval
          ? { pendingApproval: approval }
          : { pendingApproval: undefined }),
      })
      break
    }
    case "message.create":
    case "media.create": {
      const messageId = (payload.message_id as string) || `pico-${Date.now()}`
      const deliveryId =
        typeof payload.delivery_id === "string"
          ? payload.delivery_id
          : undefined
      const { content, kind, toolCalls } =
        parseAssistantMessageCreateState(payload)
      const attachments = parseAttachments(payload)
      const contextUsage = parseContextUsage(payload)
      const isPlaceholder = payload.placeholder === true
      const modelName = parseModelName(payload)
      const timestamp =
        message.timestamp !== undefined &&
        Number.isFinite(Number(message.timestamp))
          ? normalizeUnixTimestamp(Number(message.timestamp))
          : Date.now()

      updateChatStore((prev) => ({
        messages: (() => {
          const duplicate = deliveryId
            ? prev.messages.find((msg) => msg.deliveryId === deliveryId)
            : undefined
          if (duplicate) {
            return prev.messages.map((msg) =>
              msg.id === duplicate.id
                ? {
                    ...msg,
                    content,
                    kind,
                    ...(modelName ? { modelName } : {}),
                    ...(toolCalls ? { toolCalls } : {}),
                    attachments,
                    ...(deliveryId ? { deliveryId } : {}),
                  }
                : msg,
            )
          }
          return [
            ...prev.messages,
            {
              id: messageId,
              role: "assistant" as const,
              content,
              kind,
              ...(modelName ? { modelName } : {}),
              ...(toolCalls ? { toolCalls } : {}),
              attachments,
              ...(deliveryId ? { deliveryId } : {}),
              timestamp,
            },
          ]
        })(),
        isTyping:
          !isPlaceholder &&
          (kind === "normal" || message.type === "media.create")
            ? false
            : prev.isTyping,
        ...(contextUsage ? { contextUsage } : {}),
      }))
      return deliveryId
    }

    case "message.update": {
      const messageId = payload.message_id as string
      const attachments = parseAttachments(payload)
      const contextUsage = parseContextUsage(payload)
      const modelName = parseModelName(payload)
      const deliveryId =
        typeof payload.delivery_id === "string"
          ? payload.delivery_id
          : undefined
      const timestamp =
        message.timestamp !== undefined &&
        Number.isFinite(Number(message.timestamp))
          ? normalizeUnixTimestamp(Number(message.timestamp))
          : Date.now()
      if (!messageId) {
        break
      }

      updateChatStore((prev) => ({
        messages: (() => {
          let found = false
          const messages = prev.messages.map((msg) => {
            if (msg.id !== messageId) {
              return msg
            }
            found = true
            const { content, kind, toolCalls } =
              parseAssistantMessageUpdateState(payload, msg)
            return {
              ...msg,
              id: messageId,
              content,
              kind,
              toolCalls,
              ...(modelName ? { modelName } : {}),
              ...(attachments ? { attachments } : {}),
              ...(deliveryId ? { deliveryId } : {}),
            }
          })
          if (found) {
            return messages
          }

          const { content, kind, toolCalls } =
            parseAssistantMessageUpdateState(payload)

          return [
            ...messages,
            {
              id: messageId,
              role: "assistant" as const,
              content,
              kind,
              toolCalls,
              ...(modelName ? { modelName } : {}),
              ...(attachments ? { attachments } : {}),
              ...(deliveryId ? { deliveryId } : {}),
              timestamp,
            },
          ]
        })(),
        ...(contextUsage ? { contextUsage } : {}),
      }))
      return deliveryId
    }

    case "message.delete": {
      const messageId = payload.message_id as string
      if (!messageId) {
        break
      }

      updateChatStore((prev) => ({
        messages: prev.messages.filter((msg) => msg.id !== messageId),
      }))
      break
    }

    case "typing.start":
      updateChatStore({ isTyping: true })
      break

    case "typing.stop":
      updateChatStore({ isTyping: false })
      break

    case "message.ack": {
      const requestId =
        typeof payload.request_id === "string" ? payload.request_id : ""
      const status = payload.status === "duplicate" ? "duplicate" : "accepted"
      if (requestId) {
        updateChatStore((prev) => ({
          messages: prev.messages.map((entry) =>
            entry.id === requestId
              ? { ...entry, deliveryStatus: status }
              : entry,
          ),
        }))
      }
      break
    }

    case "approval.request": {
      const pendingApproval = parseApproval(payload)
      if (pendingApproval) {
        updateChatStore({ pendingApproval })
        toast.info(
          i18n.t("chat.approvalRequired", { tool: pendingApproval.tool }),
        )
      }
      break
    }

    case "approval.ack":
      break

    case "approval.resolved": {
      const approvalId =
        typeof payload.approval_id === "string" ? payload.approval_id : ""
      updateChatStore((prev) =>
        !approvalId || prev.pendingApproval?.id === approvalId
          ? { pendingApproval: undefined }
          : {},
      )
      break
    }

    case "error": {
      const requestId =
        typeof payload.request_id === "string" ? payload.request_id : ""
      const errorMessage =
        typeof payload.message === "string" ? payload.message : ""

      console.error("Pico error:", payload)
      if (errorMessage) {
        toast.error(errorMessage)
      }
      updateChatStore((prev) => ({
        messages: requestId
          ? prev.messages.map((msg) =>
              msg.id === requestId
                ? { ...msg, deliveryStatus: "failed" as const }
                : msg,
            )
          : prev.messages,
        pendingApproval:
          requestId && prev.pendingApproval?.id === requestId
            ? { ...prev.pendingApproval, isResponding: false }
            : prev.pendingApproval,
        isTyping:
          requestId && prev.pendingApproval?.id === requestId
            ? prev.isTyping
            : false,
      }))
      break
    }

    case "pong":
      break

    default:
      console.log("Unknown pico message type:", message.type)
  }

  return undefined
}
