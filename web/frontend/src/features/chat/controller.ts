import { getDefaultStore } from "jotai"
import { toast } from "sonner"

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
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(
            JSON.stringify({
              type: "turn.status.get",
              payload: {
                include_recovery:
                  firstStatusRequest || Boolean(getChatState().activeTurn),
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
              recentActivities: history.activities,
              recentPlan: history.plan,
              turnHistory: history.turnHistory,
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
        const deliveryId = handlePicoMessage(message, sessionId)
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

export async function hydrateActiveSession() {
  if (hydratePromise) {
    return hydratePromise
  }

  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  if (
    !storedSessionId ||
    state.hasHydratedActiveSession ||
    storedSessionId !== state.activeSessionId
  ) {
    if (!state.hasHydratedActiveSession) {
      updateChatStore({ hasHydratedActiveSession: true })
    }
    return
  }

  updateChatStore({ isHistoryLoading: true })
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
          recentActivities: history.activities,
          recentPlan: history.plan,
          turnHistory: history.turnHistory,
          hasHydratedActiveSession: true,
          isHistoryLoading: false,
          historyCursor: history.nextCursor,
          hasOlderHistory: history.hasOlder,
        })
        return
      }

      updateChatStore({
        messages: history.messages,
        recentActivities: history.activities,
        recentPlan: history.plan,
        turnHistory: history.turnHistory,
        isTyping: false,
        hasHydratedActiveSession: true,
        isHistoryLoading: false,
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
        updateChatStore({ hasHydratedActiveSession: true })
        return
      }

      toast.error(i18n.t("chat.historyOpenFailed"))
      updateChatStore({
        messages: [],
        isTyping: false,
        hasHydratedActiveSession: true,
        isHistoryLoading: false,
      })
    })
    .finally(() => {
      hydratePromise = null
    })

  return hydratePromise
}

interface SendChatMessageInput {
  content: string
  attachments?: ChatAttachment[]
}

export function sendChatMessage({
  content,
  attachments = [],
}: SendChatMessageInput) {
  if (!wsRef || wsRef.readyState !== WebSocket.OPEN) {
    console.warn("WebSocket not connected")
    return false
  }

  const normalizedContent = content.trim()
  const normalizedAttachments = attachments
    .filter((attachment) => attachment.type === "image" && attachment.url)
    .map((attachment) => ({ ...attachment }))

  if (!normalizedContent && normalizedAttachments.length === 0) {
    return false
  }

  const socket = wsRef
  const id = `msg-${++msgIdCounter}-${Date.now()}`

  updateChatStore((prev) => ({
    messages: [
      ...prev.messages,
      {
        id,
        role: "user",
        content: normalizedContent,
        attachments:
          normalizedAttachments.length > 0 ? normalizedAttachments : undefined,
        timestamp: Date.now(),
        deliveryStatus: "sending",
      },
    ],
    isTyping: true,
    interrupted: false,
  }))

  try {
    const payload: Record<string, unknown> = {
      content: normalizedContent,
      media: normalizedAttachments.map((attachment) => attachment.url),
    }

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
  const cursor = state.historyCursor
  updateChatStore({ isLoadingOlderHistory: true })
  try {
    const history = await loadOlderSessionHistory(sessionId, cursor)
    if (getChatState().activeSessionId !== sessionId) {
      return false
    }
    updateChatStore((prev) => ({
      messages: mergeHistoryMessages(history.messages, prev.messages),
      historyCursor: history.nextCursor,
      hasOlderHistory: history.hasOlder,
      isLoadingOlderHistory: false,
    }))
    return history.messages.length > 0
  } catch (error) {
    console.error("Failed to load earlier messages:", error)
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
  if (sessionId === activeSessionIdRef) {
    return
  }

  try {
    updateChatStore({ isHistoryLoading: true })
    const history = await loadSessionHistory(sessionId)

    disconnectChatInternal({ clearDesiredConnection: false })
    setActiveSessionId(sessionId)
    updateChatStore({
      messages: history.messages,
      recentActivities: history.activities,
      recentPlan: history.plan,
      turnHistory: history.turnHistory,
      isTyping: false,
      hasHydratedActiveSession: true,
      isHistoryLoading: false,
      contextUsage: undefined,
      historyCursor: history.nextCursor,
      hasOlderHistory: history.hasOlder,
    })

    if (store.get(gatewayAtom).status === "running") {
      shouldMaintainConnection = true
      await connectChat()
    }
  } catch (error) {
    updateChatStore({ isHistoryLoading: false })
    console.error("Failed to load session history:", error)
    toast.error(i18n.t("chat.historyOpenFailed"))
  }
}

export async function newChatSession() {
  if (getChatState().messages.length === 0) {
    return
  }

  disconnectChatInternal({ clearDesiredConnection: false })
  setActiveSessionId(generateSessionId())
  updateChatStore({
    messages: [],
    isTyping: false,
    hasHydratedActiveSession: true,
    isHistoryLoading: false,
    turnHistory: undefined,
    contextUsage: undefined,
    historyCursor: undefined,
    hasOlderHistory: false,
    isLoadingOlderHistory: false,
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
