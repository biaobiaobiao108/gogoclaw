import { describe, expect, test } from "bun:test"

import { loadSessionHistory, mergeHistoryMessages } from "./history"

describe("mergeHistoryMessages", () => {
  test("reconciles messages received during a disconnect without duplicates", () => {
    const first = {
      id: "one",
      role: "user" as const,
      content: "hello",
      timestamp: 1,
    }
    const second = {
      id: "two",
      role: "assistant" as const,
      content: "done",
      timestamp: 2,
    }

    expect(mergeHistoryMessages([first, second], [first])).toEqual([
      first,
      second,
    ])
  })
})

test("stable persisted IDs reconcile server timestamps and accepted delivery", async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = (async () =>
    Response.json({
      messages: [
        {
          message_id: "client-id",
          role: "user",
          content: "hello",
          created_at: "2026-11-09T03:00:00.050Z",
        },
      ],
      updated: "2026-11-09",
    })) as unknown as typeof fetch
  try {
    const history = await loadSessionHistory("s")
    const merged = mergeHistoryMessages(history.messages, [
      {
        id: "client-id",
        role: "user",
        content: "hello",
        timestamp: Date.parse("2026-11-09T03:00:00Z"),
        deliveryStatus: "unknown",
      },
    ])
    expect(merged).toHaveLength(1)
    expect(merged[0].deliveryStatus).toBe("accepted")
    expect(merged[0].timestamp).toBe("2026-11-09T03:00:00.050Z")
  } finally {
    globalThis.fetch = originalFetch
  }
})

test("identical repeated messages retain separate stable IDs", () => {
  const first = {
    id: "first",
    role: "user" as const,
    content: "continue",
    timestamp: 1,
  }
  const second = { ...first, id: "second", timestamp: 2 }
  expect(mergeHistoryMessages([first, second], [first])).toHaveLength(2)
})
