import { describe, expect, test } from "bun:test"

import { mergeHistoryMessages } from "./history"

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
