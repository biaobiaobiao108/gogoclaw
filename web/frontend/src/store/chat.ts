import { atom, getDefaultStore } from "jotai"
import { atomWithStorage } from "jotai/utils"

import {
  ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
  type AssistantDetailVisibility,
  assistantDetailVisibilityStorage,
  DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
  shouldShowAssistantMessage,
} from "@/features/chat/detail-visibility"
import {
  getInitialActiveSessionId,
  writeStoredSessionId,
} from "@/features/chat/state"

export interface ChatAttachment {
  type: "image" | "audio" | "video" | "file"
  url: string
  filename?: string
  contentType?: string
}

export interface ChatToolCallFunction {
  name?: string
  arguments?: string
}

export interface ChatToolCallExtraContent {
  toolFeedbackExplanation?: string
}

export interface ChatToolCall {
  id?: string
  type?: string
  function?: ChatToolCallFunction
  extraContent?: ChatToolCallExtraContent
}

export type AssistantMessageKind = "normal" | "thought" | "tool_calls"
export type MessageDeliveryStatus =
  "sending" | "accepted" | "duplicate" | "failed" | "unknown"

export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string
  timestamp: number | string
  kind?: AssistantMessageKind
  modelName?: string
  attachments?: ChatAttachment[]
  toolCalls?: ChatToolCall[]
  deliveryId?: string
  deliveryStatus?: MessageDeliveryStatus
}

export interface ContextUsage {
  used_tokens: number
  total_tokens: number
  history_tokens?: number
  compress_at_tokens: number
  summarize_at_tokens?: number
  used_percent: number
}

export interface ChatActivity {
  id: string
  kind: string
  status: string
  title: string
  toolCallId?: string
  tool?: string
  detail?: string
  output?: string
  durationMs?: number
  exitCode?: number
  diff?: string
  artifacts?: string[]
  startedAt: string
  completedAt?: string
}

export interface ChatPlanStep {
  text: string
  status: string
}

export interface ChatTurnHistory {
  turnId: string
  status: string
  iteration: number
  plan: ChatPlanStep[]
  activities: ChatActivity[]
}

export interface ChatApproval {
  id: string
  tool: string
  arguments?: Record<string, unknown>
  status?: string
  risk?: string
  createdAt?: number
  expiresAt?: number
  isResponding?: boolean
}

export type ConnectionState =
  "disconnected" | "connecting" | "connected" | "error"

export interface ChatStoreState {
  messages: ChatMessage[]
  connectionState: ConnectionState
  isTyping: boolean
  activeTurn?: {
    turnId: string
    phase: string
    iteration: number
    plan?: { text: string; status: string }[]
  }
  recentActivities?: ChatActivity[]
  recentPlan?: ChatPlanStep[]
  turnHistory?: ChatTurnHistory[]
  pendingApproval?: ChatApproval
  interrupted?: boolean
  activeSessionId: string
  hasHydratedActiveSession: boolean
  isHistoryLoading: boolean
  historyError?: boolean
  isLoadingOlderHistory?: boolean
  historyCursor?: string
  hasOlderHistory?: boolean
  contextUsage?: ContextUsage
}

type ChatStorePatch = Partial<ChatStoreState>

const DEFAULT_CHAT_STATE: ChatStoreState = {
  messages: [],
  connectionState: "disconnected",
  isTyping: false,
  activeSessionId: getInitialActiveSessionId(),
  hasHydratedActiveSession: false,
  isHistoryLoading: false,
  historyError: false,
}

export const chatAtom = atom<ChatStoreState>(DEFAULT_CHAT_STATE)
export const assistantDetailVisibilityAtom =
  atomWithStorage<AssistantDetailVisibility>(
    ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
    DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
    assistantDetailVisibilityStorage,
    { getOnInit: true },
  )
export const showAssistantDetailsAtom = atom(
  (get) => get(assistantDetailVisibilityAtom) !== "none",
)

const store = getDefaultStore()

export function getChatState() {
  return store.get(chatAtom)
}

export function updateChatStore(
  patch:
    | ChatStorePatch
    | ((prev: ChatStoreState) => ChatStorePatch | ChatStoreState),
) {
  store.set(chatAtom, (prev) => {
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    const next = { ...prev, ...nextPatch }

    if (next.activeSessionId !== prev.activeSessionId) {
      writeStoredSessionId(next.activeSessionId)
    }

    return next
  })
}

export { shouldShowAssistantMessage, DEFAULT_ASSISTANT_DETAIL_VISIBILITY }
export type { AssistantDetailVisibility }
