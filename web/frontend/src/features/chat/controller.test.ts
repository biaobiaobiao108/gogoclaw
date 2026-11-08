import { expect, test } from "bun:test"

import { getChatState } from "@/store/chat"

import { newChatSession, switchChatSession } from "./controller"

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
