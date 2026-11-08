import { expect, test } from "bun:test"

import { getChatState, updateChatStore } from "@/store/chat"

import {
  loadOlderChatHistory,
  newChatSession,
  switchChatSession,
} from "./controller"

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
