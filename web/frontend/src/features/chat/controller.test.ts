import { expect, test } from "bun:test"

import { getDefaultStore } from "jotai"

import { getChatState, updateChatStore } from "@/store/chat"
import { gatewayAtom } from "@/store/gateway"

import {
  connectChat,
  disconnectChat,
  loadOlderChatHistory,
  newChatSession,
  refreshActiveSessionHistory,
  sendChatMessage,
  switchChatSession,
} from "./controller"

async function withConnectedChat(run: () => Promise<void>) {
  const originalSocket = globalThis.WebSocket
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window")
  const originalGateway = getDefaultStore().get(gatewayAtom)
  class Socket {
    static OPEN = 1
    static CONNECTING = 0
    readyState = 1
    onopen = null
    onmessage = null
    onclose = null
    onerror = null
    send() {}
    close() {}
  }
  globalThis.WebSocket = Socket as unknown as typeof WebSocket
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { location: { protocol: "http:", host: "localhost" } },
  })
  getDefaultStore().set(gatewayAtom, { ...originalGateway, status: "running" })
  updateChatStore({
    hasHydratedActiveSession: true,
    isTyping: false,
    activeTurn: undefined,
  })
  try {
    await connectChat()
    await run()
  } finally {
    disconnectChat()
    globalThis.WebSocket = originalSocket
    if (originalWindow)
      Object.defineProperty(globalThis, "window", originalWindow)
    else Reflect.deleteProperty(globalThis, "window")
    getDefaultStore().set(gatewayAtom, originalGateway)
  }
}

test("editing replaces the target message without retaining its old copy", async () => {
  await withConnectedChat(async () => {
    updateChatStore({
      messages: [
        { id: "user", role: "user", content: "old", timestamp: 1 },
        { id: "answer", role: "assistant", content: "answer", timestamp: 2 },
      ],
    })
    expect(
      sendChatMessage({
        content: "edited",
        action: "edit",
        targetMessageId: "user",
      }),
    ).toBe(true)
    expect(getChatState().messages).toHaveLength(1)
    expect(getChatState().messages[0]).toMatchObject({
      id: "user",
      content: "edited",
      deliveryStatus: "sending",
    })
  })
})

test("a history refresh cannot overwrite a message sent while it was pending", async () => {
  const originalFetch = globalThis.fetch
  let finish!: (response: Response) => void
  globalThis.fetch = ((_url: string | URL | Request) =>
    new Promise<Response>((resolve) => {
      finish = resolve
    })) as typeof fetch
  try {
    await withConnectedChat(async () => {
      updateChatStore({ messages: [] })
      const refresh = refreshActiveSessionHistory()
      expect(sendChatMessage({ content: "next question" })).toBe(true)
      updateChatStore({
        activeTurn: { turnId: "new-turn", phase: "llm", iteration: 1 },
        pendingApproval: { id: "approval", tool: "exec" },
      })
      finish(Response.json({ messages: [], updated: "2026-01-01" }))
      await refresh
      expect(getChatState().messages[0]?.content).toBe("next question")
      expect(getChatState().activeTurn?.turnId).toBe("new-turn")
      expect(getChatState().pendingApproval?.id).toBe("approval")
      expect(getChatState().isTyping).toBe(true)
    })
  } finally {
    globalThis.fetch = originalFetch
  }
})

test("the latest history refresh wins when responses arrive out of order", async () => {
  const originalFetch = globalThis.fetch
  const pending: ((response: Response) => void)[] = []
  globalThis.fetch = ((_url: string | URL | Request) =>
    new Promise<Response>((resolve) => {
      pending.push(resolve)
    })) as typeof fetch
  updateChatStore({ messages: [], isTyping: false, activeTurn: undefined })
  try {
    const first = refreshActiveSessionHistory()
    const second = refreshActiveSessionHistory()
    pending[1](
      Response.json({
        messages: [
          { message_id: "latest", role: "assistant", content: "latest" },
        ],
        updated: "2026-01-01",
      }),
    )
    await second
    pending[0](Response.json({ messages: [], updated: "2026-01-01" }))
    await first
    expect(getChatState().messages[0]?.id).toBe("latest")
  } finally {
    globalThis.fetch = originalFetch
  }
})

test("settled history clears a stale typing indicator when no new work arrives", async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = ((_url: string | URL | Request) =>
    Promise.resolve(
      Response.json({
        messages: [
          { message_id: "recovered", role: "assistant", content: "finished" },
        ],
        updated: "2026-01-01",
      }),
    )) as typeof fetch
  try {
    updateChatStore({ messages: [], isTyping: true, activeTurn: undefined })
    await refreshActiveSessionHistory()
    expect(getChatState().messages[0]?.id).toBe("recovered")
    expect(getChatState().isTyping).toBe(false)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test("switching sessions clears history errors and ignores stale pagination", async () => {
  const originalFetch = globalThis.fetch
  let finishOlder!: (response: Response) => void
  globalThis.fetch = ((url: string | URL | Request) => {
    if (String(url).includes("cursor="))
      return new Promise<Response>((resolve) => {
        finishOlder = resolve
      })
    return Promise.resolve(
      Response.json({ messages: [], updated: "2026-01-01" }),
    )
  }) as typeof fetch
  try {
    updateChatStore({
      historyCursor: "old",
      hasOlderHistory: true,
      historyError: true,
    })
    const older = loadOlderChatHistory()
    await switchChatSession("pagination-target")
    expect(getChatState().historyError).toBe(false)
    expect(getChatState().isLoadingOlderHistory).toBe(false)
    updateChatStore({ isLoadingOlderHistory: true })
    finishOlder(Response.json({ messages: [], updated: "2026-01-01" }))
    expect(await older).toBe(false)
    expect(getChatState().isLoadingOlderHistory).toBe(true)
  } finally {
    updateChatStore({ isLoadingOlderHistory: false })
    globalThis.fetch = originalFetch
  }
})

test("new chat creates a distinct session even when the current history is empty", async () => {
  updateChatStore({ messages: [] })
  const previous = getChatState().activeSessionId
  await newChatSession()
  expect(getChatState().activeSessionId).not.toBe(previous)
})

test("the latest session selection wins when history requests finish out of order", async () => {
  const originalFetch = globalThis.fetch
  const pending = new Map<string, (response: Response) => void>()
  globalThis.fetch = ((url: string | URL | Request) =>
    new Promise<Response>((resolve) => {
      pending.set(String(url), resolve)
    })) as typeof fetch
  try {
    const first = switchChatSession("review-session-a")
    const second = switchChatSession("review-session-b")
    const resolve = (id: string) => {
      const entry = [...pending.entries()].find(([url]) => url.includes(id))
      if (!entry) throw new Error(`missing request for ${id}`)
      entry[1](Response.json({ messages: [], updated: "2026-01-01" }))
    }
    resolve("review-session-b")
    await second
    resolve("review-session-a")
    await first
    expect(getChatState().activeSessionId).toBe("review-session-b")
    expect(getChatState().isHistoryLoading).toBe(false)
    await newChatSession()
  } finally {
    globalThis.fetch = originalFetch
  }
})
