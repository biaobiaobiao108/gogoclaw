import { getDefaultStore } from "jotai"
import { toast } from "sonner"

import { setSessionFeedback } from "@/api/sessions"
import {
  loadOlderSessionHistory,
  loadSessionHistory,
  mergeHistoryMessages,
} from "@/features/chat/history"
import { handlePicoMessage, type PicoMessage } from "@/features/chat/protocol"
import { generateSessionId, readStoredSessionId } from "@/features/chat/state"
import { invalidateSocket, isCurrentSocket } from "@/features/chat/websocket"
import i18n from "@/i18n"
import {
  type ChatAttachment,
  type ChatFeedbackValue,
  getChatState,
  updateChatStore,
} from "@/store/chat"
import { gatewayAtom, type GatewayState } from "@/store/gateway"

const store = getDefaultStore()

let wsRef: WebSocket | null = null
let isConnecting = false
let msgIdCounter = 0
let activeSessionIdRef = getChatState().activeSessionId
let initialized = false
let unsubscribeGateway: (() => void) | null = null
let hydratePromise: Promise<void> | null = null
let connectionGeneration = 0
let reconnectTimer: number | null = null
let turnStatusTimer: number | null = null
let reconnectAttempts = 0
let shouldMaintainConnection = false
let sessionSelectionGeneration = 0
let historyGeneration = 0

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

function shouldReconnectFor(generation: number, sessionId: string): boolean {
  return (
    shouldMaintainConnection &&
    generation === connectionGeneration &&
    sessionId === activeSessionIdRef &&
    store.get(gatewayAtom).status === "running"
  )
}

function scheduleReconnect(generation: number, sessionId: string) {
  if (!shouldReconnectFor(generation, sessionId) || reconnectTimer !== null) {
    return
  }

  const delay = Math.min(1000 * 2 ** reconnectAttempts, 5000)
  reconnectAttempts += 1
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    if (!shouldReconnectFor(generation, sessionId)) {
      return
    }
    void connectChat()
  }, delay)
}

function needsActiveSessionHydration(): boolean {
  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  return Boolean(
    storedSessionId &&
    storedSessionId === state.activeSessionId &&
    !state.hasHydratedActiveSession,
  )
}

function setActiveSessionId(sessionId: string) {
  activeSessionIdRef = sessionId
  updateChatStore({ activeSessionId: sessionId })
}

function disconnectChatInternal({
  clearDesiredConnection,
}: {
  clearDesiredConnection: boolean
}) {
  connectionGeneration += 1
  clearReconnectTimer()
  if (turnStatusTimer !== null) {
    window.clearInterval(turnStatusTimer)
    turnStatusTimer = null
  }

  if (clearDesiredConnection) {
    shouldMaintainConnection = false
  }

  const socket = wsRef
  wsRef = null
  isConnecting = false

  invalidateSocket(socket)

  updateChatStore({
    connectionState: "disconnected",
    isTyping: false,
    activeTurn: undefined,
    pendingApproval: undefined,
    interrupted: false,
  })
  updateChatStore((prev) => ({
    messages: prev.messages.map((message) =>
      message.deliveryStatus === "sending"
        ? { ...message, deliveryStatus: "unknown" }
        : message,
    ),
  }))
}

export async function connectChat() {
  if (
    store.get(gatewayAtom).status !== "running" ||
    needsActiveSessionHydration()
  ) {
    return
  }

  if (
    isConnecting ||
    (wsRef &&
      (wsRef.readyState === WebSocket.OPEN ||
        wsRef.readyState === WebSocket.CONNECTING))
  ) {
    return
  }

  const generation = connectionGeneration + 1
  connectionGeneration = generation
  isConnecting = true
  clearReconnectTimer()
  updateChatStore({ connectionState: "connecting" })

  try {
    const sessionId = activeSessionIdRef

    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }

    const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:"
    const wsUrl = `${wsScheme}//${window.location.host}/pico/ws`
    const url = `${wsUrl}?session_id=${encodeURIComponent(sessionId)}`
    const socket = new WebSocket(url)

    if (generation !== connectionGeneration) {
      isConnecting = false
      invalidateSocket(socket)
      return
    }

    socket.onopen = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      updateChatStore({ connectionState: "connected" })
      isConnecting = false
      let firstStatusRequest = true
      const requestTurnStatus = () => {
        const activeTurn = Boolean(getChatState().activeTurn)
        if (!firstStatusRequest && !activeTurn) return
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(
            JSON.stringify({
              type: "turn.status.get",
              payload: {
                include_recovery: firstStatusRequest || activeTurn,
              },
            }),
          )
          firstStatusRequest = false
        }
      }
      requestTurnStatus()
      turnStatusTimer = window.setInterval(requestTurnStatus, 2000)
      const reconnected = reconnectAttempts > 0
      reconnectAttempts = 0
      // Reconcile messages produced while the socket was disconnected.
      if (reconnected)
        void loadSessionHistory(sessionId)
          .then((history) => {
            if (
              !isCurrentSocket({
                socket,
                currentSocket: wsRef,
                generation,
                currentGeneration: connectionGeneration,
                sessionId,
                currentSessionId: activeSessionIdRef,
              })
            ) {
              return
            }
            updateChatStore((prev) => ({
              messages: mergeHistoryMessages(history.messages, prev.messages),
              ...(history.contextUsage
                ? { contextUsage: history.contextUsage }
                : {}),
              recentActivities: history.activities,
              recentPlan: history.plan,
              turnHistory: history.turnHistory,
              branches: history.branches ?? [],
              historyCursor: history.nextCursor,
              hasOlderHistory: history.hasOlder,
            }))
          })
          .catch((error) => {
            console.warn(
              "Failed to reconcile chat history after reconnect:",
              error,
            )
          })
    }

    socket.onmessage = (event) => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }

      try {
        const message = JSON.parse(event.data) as PicoMessage
        const previousState = getChatState()
        const wasActive =
          Boolean(previousState.activeTurn) || previousState.isTyping
        const deliveryId = handlePicoMessage(message, sessionId, {
          onBranchUpdated: () => void refreshActiveSessionHistory(sessionId),
          onTurnSettled: () => {
            if (wasActive) void refreshActiveSessionHistory(sessionId)
          },
        })
        if (deliveryId && socket.readyState === WebSocket.OPEN) {
          socket.send(
            JSON.stringify({
              type: "delivery.ack",
              payload: { delivery_id: deliveryId },
            }),
          )
        }
      } catch {
        console.warn("Non-JSON message from pico:", event.data)
      }
    }

    socket.onclose = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      wsRef = null
      isConnecting = false
      if (turnStatusTimer !== null) {
        window.clearInterval(turnStatusTimer)
        turnStatusTimer = null
      }
      updateChatStore({
        connectionState: "disconnected",
        isTyping: false,
        activeTurn: undefined,
      })
      updateChatStore((prev) => ({
        messages: prev.messages.map((message) =>
          message.deliveryStatus === "sending"
            ? { ...message, deliveryStatus: "unknown" }
            : message,
        ),
      }))
      scheduleReconnect(generation, sessionId)
    }

    socket.onerror = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      isConnecting = false
      updateChatStore({ connectionState: "error" })
      scheduleReconnect(generation, sessionId)
    }

    wsRef = socket
  } catch (error) {
    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }
    console.error("Failed to connect to pico:", error)
    updateChatStore({ connectionState: "error" })
    isConnecting = false
    scheduleReconnect(generation, activeSessionIdRef)
  }
}

export function disconnectChat() {
  disconnectChatInternal({ clearDesiredConnection: true })
}

export async function hydrateActiveSession({
  force = false,
}: { force?: boolean } = {}) {
  if (hydratePromise) {
    return hydratePromise
  }

  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  if (
    !storedSessionId ||
    (!force && state.hasHydratedActiveSession) ||
    storedSessionId !== state.activeSessionId
  ) {
    if (!state.hasHydratedActiveSession) {
      updateChatStore({ hasHydratedActiveSession: true })
    }
    return
  }

  updateChatStore({ isHistoryLoading: true, historyError: false })
  hydratePromise = loadSessionHistory(storedSessionId)
    .then((history) => {
      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({
          messages: mergeHistoryMessages(
            history.messages,
            currentState.messages,
          ),
          ...(history.contextUsage
            ? { contextUsage: history.contextUsage }
            : {}),
          recentActivities: history.activities,
          recentPlan: history.plan,
          turnHistory: history.turnHistory,
          branches: history.branches ?? [],
          hasHydratedActiveSession: true,
          isHistoryLoading: false,
          historyError: false,
          historyCursor: history.nextCursor,
          hasOlderHistory: history.hasOlder,
        })
        return
      }

      updateChatStore({
        messages: history.messages,
        ...(history.contextUsage ? { contextUsage: history.contextUsage } : {}),
        recentActivities: history.activities,
        recentPlan: history.plan,
        turnHistory: history.turnHistory,
        branches: history.branches ?? [],
        isTyping: false,
        hasHydratedActiveSession: true,
        isHistoryLoading: false,
        historyError: false,
        historyCursor: history.nextCursor,
        hasOlderHistory: history.hasOlder,
      })
    })
    .catch((error) => {
      console.error("Failed to restore last session history:", error)

      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({
          hasHydratedActiveSession: true,
          isHistoryLoading: false,
          historyError: true,
        })
        return
      }

      toast.error(i18n.t("chat.historyOpenFailed"))
      updateChatStore({
        messages: [],
        isTyping: false,
        hasHydratedActiveSession: true,
        isHistoryLoading: false,
        historyError: true,
      })
    })
    .finally(() => {
      hydratePromise = null
    })

  return hydratePromise
}

export function retryActiveSessionHistory() {
  return hydrateActiveSession({ force: true })
}

interface SendChatMessageInput {
  content: string
  attachments?: ChatAttachment[]
  thinkingLevel?: string
  action?: "send" | "regenerate" | "edit"
  targetMessageId?: string
}

export function sendChatMessage({
  content,
  attachments = [],
  thinkingLevel,
  action = "send",
  targetMessageId,
}: SendChatMessageInput) {
  if (!wsRef || wsRef.readyState !== WebSocket.OPEN) {
    console.warn("WebSocket not connected")
    return false
  }

  const normalizedContent = content.trim()
  const normalizedAttachments = attachments
    .filter(
      (attachment) =>
        attachment.url &&
        (attachment.status === undefined || attachment.status === "ready"),
    )
    .map((attachment) => ({ ...attachment }))

  if (
    action === "send" &&
    !normalizedContent &&
    normalizedAttachments.length === 0
  ) {
    return false
  }

  const socket = wsRef
  const id = `msg-${++msgIdCounter}-${Date.now()}`

  updateChatStore((prev) => {
    const targetIndex = targetMessageId
      ? prev.messages.findIndex((message) => message.id === targetMessageId)
      : -1
    if (action === "regenerate") {
      return {
        messages:
          targetIndex >= 0
            ? prev.messages.slice(0, targetIndex)
            : prev.messages,
        isTyping: true,
        interrupted: false,
        activeTurn: undefined,
      }
    }
    if (action === "edit") {
      return {
        messages:
          targetIndex >= 0
            ? [
                ...prev.messages.slice(0, targetIndex),
                {
                  ...prev.messages[targetIndex],
                  content: normalizedContent,
                  attachments:
                    normalizedAttachments.length > 0
                      ? normalizedAttachments
                      : undefined,
                  deliveryStatus: "sending" as const,
                },
              ]
            : prev.messages,
        isTyping: true,
        interrupted: false,
        activeTurn: undefined,
      }
    }
    return {
      messages: [
        ...prev.messages,
        {
          id,
          role: "user",
          content: normalizedContent,
          attachments:
            normalizedAttachments.length > 0
              ? normalizedAttachments
              : undefined,
          timestamp: Date.now(),
          deliveryStatus: "sending",
        },
      ],
      isTyping: true,
      interrupted: false,
    }
  })

  try {
    const payload: Record<string, unknown> = {
      content: normalizedContent,
      media: normalizedAttachments
        .filter((attachment) => attachment.url.startsWith("data:image/"))
        .map((attachment) => attachment.url),
      attachments: normalizedAttachments.map((attachment) => ({
        type: attachment.type,
        url: attachment.url,
        ...(attachment.path ? { path: attachment.path } : {}),
        ...(attachment.filename ? { filename: attachment.filename } : {}),
        ...(attachment.contentType
          ? { content_type: attachment.contentType }
          : {}),
      })),
    }
    if (thinkingLevel) payload.thinking_level = thinkingLevel
    if (action === "regenerate" && targetMessageId)
      payload.regenerate_from_message_id = targetMessageId
    if (action === "edit" && targetMessageId)
      payload.edit_message_id = targetMessageId

    socket.send(
      JSON.stringify({
        type: "message.send",
        id,
        payload,
      }),
    )
    return true
  } catch (error) {
    console.error("Failed to send pico message:", error)
    updateChatStore((prev) => ({
      messages: prev.messages.filter((message) => message.id !== id),
      isTyping: false,
    }))
    return false
  }
}

export function retryChatMessage(id: string) {
  const message = getChatState().messages.find((item) => item.id === id)
  if (!message || !wsRef || wsRef.readyState !== WebSocket.OPEN) return false
  try {
    wsRef.send(
      JSON.stringify({
        type: "message.send",
        id,
        payload: {
          content: message.content,
          media: (message.attachments ?? [])
            .filter((item) => item.url.startsWith("data:image/"))
            .map((item) => item.url),
          attachments: (message.attachments ?? []).map((item) => ({
            type: item.type,
            url: item.url,
            ...(item.path ? { path: item.path } : {}),
            ...(item.filename ? { filename: item.filename } : {}),
            ...(item.contentType ? { content_type: item.contentType } : {}),
          })),
        },
      }),
    )
    updateChatStore((prev) => ({
      messages: prev.messages.map((item) =>
        item.id === id ? { ...item, deliveryStatus: "sending" } : item,
      ),
    }))
    return true
  } catch (error) {
    console.error("Failed to retry pico message:", error)
    toast.error(i18n.t("chat.deliveryRetryFailed"))
    return false
  }
}

export async function refreshActiveSessionHistory(sessionId?: string) {
  const expectedSessionId = sessionId ?? activeSessionIdRef
  const selection = sessionSelectionGeneration
  try {
    const history = await loadSessionHistory(expectedSessionId)
    if (
      getChatState().activeSessionId !== expectedSessionId ||
      selection !== sessionSelectionGeneration
    ) {
      return
    }
    updateChatStore({
      messages: history.messages,
      ...(history.contextUsage ? { contextUsage: history.contextUsage } : {}),
      recentActivities: history.activities,
      recentPlan: history.plan,
      turnHistory: history.turnHistory,
      branches: history.branches ?? [],
      historyCursor: history.nextCursor,
      hasOlderHistory: history.hasOlder,
      isTyping: false,
      activeTurn: undefined,
      pendingApproval: undefined,
      historyError: false,
    })
  } catch (error) {
    console.error("Failed to refresh chat history:", error)
  }
}

export function selectChatBranch(branchId: string) {
  if (!wsRef || wsRef.readyState !== WebSocket.OPEN) {
    toast.error(i18n.t("chat.branchSwitchFailed"))
    return false
  }
  try {
    wsRef.send(
      JSON.stringify({
        type: "branch.select",
        payload: { branch_id: branchId },
      }),
    )
    return true
  } catch (error) {
    console.error("Failed to select chat branch:", error)
    toast.error(i18n.t("chat.branchSwitchFailed"))
    return false
  }
}

export async function saveMessageFeedback(
  messageId: string,
  value: ChatFeedbackValue,
) {
  const sessionId = getChatState().activeSessionId
  const message = getChatState().messages.find((item) => item.id === messageId)
  if (!message) return
  const nextValue = message.feedback === value ? undefined : value
  updateChatStore((prev) => ({
    messages: prev.messages.map((item) =>
      item.id === messageId ? { ...item, feedback: nextValue } : item,
    ),
  }))
  try {
    await setSessionFeedback(sessionId, messageId, nextValue ?? "")
  } catch (error) {
    console.error("Failed to save message feedback:", error)
    updateChatStore((prev) => ({
      messages: prev.messages.map((item) =>
        item.id === messageId ? { ...item, feedback: message.feedback } : item,
      ),
    }))
    toast.error(i18n.t("chat.feedbackSaveFailed"))
  }
}

export async function loadOlderChatHistory(): Promise<boolean> {
  const state = getChatState()
  if (
    !state.historyCursor ||
    !state.hasOlderHistory ||
    state.isLoadingOlderHistory
  ) {
    return false
  }
  const sessionId = state.activeSessionId
  const selection = historyGeneration
  const cursor = state.historyCursor
  updateChatStore({ isLoadingOlderHistory: true })
  try {
    const history = await loadOlderSessionHistory(sessionId, cursor)
    if (
      getChatState().activeSessionId !== sessionId ||
      selection !== historyGeneration
    ) {
      return false
    }
    updateChatStore((prev) => ({
      messages: mergeHistoryMessages(history.messages, prev.messages),
      historyCursor: history.nextCursor,
      hasOlderHistory: history.hasOlder,
      historyError: false,
      isLoadingOlderHistory: false,
    }))
    return history.messages.length > 0
  } catch (error) {
    console.error("Failed to load earlier messages:", error)
    if (
      getChatState().activeSessionId !== sessionId ||
      selection !== historyGeneration
    )
      return false
    updateChatStore({ isLoadingOlderHistory: false })
    toast.error(i18n.t("chat.historyLoadFailed"))
    return false
  }
}

export function respondToApproval({
  approve,
  scope = "once",
}: {
  approve: boolean
  scope?: "once" | "session"
}) {
  const pendingApproval = getChatState().pendingApproval
  if (
    !pendingApproval ||
    pendingApproval.isResponding ||
    !wsRef ||
    wsRef.readyState !== WebSocket.OPEN
  ) {
    return false
  }

  updateChatStore({
    pendingApproval: { ...pendingApproval, isResponding: true },
  })
  try {
    wsRef.send(
      JSON.stringify({
        type: "approval.respond",
        id: pendingApproval.id,
        payload: {
          approval_id: pendingApproval.id,
          approve,
          scope: approve ? scope : "once",
        },
      }),
    )
    return true
  } catch (error) {
    console.error("Failed to respond to approval:", error)
    updateChatStore({
      pendingApproval: { ...pendingApproval, isResponding: false },
    })
    toast.error(i18n.t("chat.approvalSendFailed"))
    return false
  }
}

export async function switchChatSession(sessionId: string) {
  const selection = ++sessionSelectionGeneration
  if (sessionId === activeSessionIdRef) {
    updateChatStore({ isHistoryLoading: false })
    return
  }

  try {
    updateChatStore({ isHistoryLoading: true })
    const history = await loadSessionHistory(sessionId)

    if (selection !== sessionSelectionGeneration) return

    historyGeneration += 1
    disconnectChatInternal({ clearDesiredConnection: false })
    setActiveSessionId(sessionId)
    updateChatStore({
      messages: history.messages,
      ...(history.contextUsage ? { contextUsage: history.contextUsage } : {}),
      recentActivities: history.activities,
      recentPlan: history.plan,
      turnHistory: history.turnHistory,
      branches: history.branches ?? [],
      isTyping: false,
      hasHydratedActiveSession: true,
      isHistoryLoading: false,
      contextUsage: history.contextUsage,
      historyCursor: history.nextCursor,
      hasOlderHistory: history.hasOlder,
      isLoadingOlderHistory: false,
      historyError: false,
    })

    if (store.get(gatewayAtom).status === "running") {
      shouldMaintainConnection = true
      await connectChat()
    }
  } catch (error) {
    if (selection !== sessionSelectionGeneration) return
    updateChatStore({ isHistoryLoading: false })
    console.error("Failed to load session history:", error)
    toast.error(i18n.t("chat.historyOpenFailed"))
  }
}

export async function newChatSession() {
  sessionSelectionGeneration += 1
  historyGeneration += 1
  disconnectChatInternal({ clearDesiredConnection: false })
  setActiveSessionId(generateSessionId())
  updateChatStore({
    messages: [],
    isTyping: false,
    hasHydratedActiveSession: true,
    isHistoryLoading: false,
    recentActivities: undefined,
    recentPlan: undefined,
    turnHistory: undefined,
    branches: undefined,
    contextUsage: undefined,
    historyCursor: undefined,
    hasOlderHistory: false,
    isLoadingOlderHistory: false,
    historyError: false,
  })

  if (store.get(gatewayAtom).status === "running") {
    shouldMaintainConnection = true
    await connectChat()
  }
}

export function initializeChatStore() {
  if (initialized) {
    return
  }

  initialized = true
  activeSessionIdRef = getChatState().activeSessionId
  let lastGatewayStatus: GatewayState | null = null

  const syncConnectionWithGateway = (force: boolean = false) => {
    const gatewayStatus = store.get(gatewayAtom).status
    if (!force && gatewayStatus === lastGatewayStatus) {
      return
    }
    lastGatewayStatus = gatewayStatus

    if (gatewayStatus === "running") {
      shouldMaintainConnection = true
      if (needsActiveSessionHydration()) {
        return
      }
      void connectChat()
      return
    }

    if (gatewayStatus === "stopped" || gatewayStatus === "error") {
      disconnectChatInternal({ clearDesiredConnection: true })
    }
  }

  unsubscribeGateway = store.sub(gatewayAtom, syncConnectionWithGateway)

  if (!readStoredSessionId()) {
    updateChatStore({ hasHydratedActiveSession: true })
    syncConnectionWithGateway(true)
    return
  }

  void hydrateActiveSession().finally(() => {
    if (!initialized) {
      return
    }
    syncConnectionWithGateway(true)
  })
}

export function teardownChatStore() {
  unsubscribeGateway?.()
  unsubscribeGateway = null
  initialized = false
  disconnectChat()
}
