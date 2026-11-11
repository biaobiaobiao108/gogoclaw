import { describe, expect, test } from "bun:test"

import type { ChatMessage } from "@/store/chat"

import {
  type ChatStreamRow,
  chatStreamRowKey,
  estimateChatStreamRow,
  resolveChatInputDisabledReason,
} from "./chat-page-logic"

const userMessage: ChatMessage = {
  id: "user-1",
  role: "user",
  content: "hello",
  timestamp: 1,
}

describe("chat page logic", () => {
  test("keeps gateway readiness ahead of websocket and model errors", () => {
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: false,
        connectionState: "disconnected",
        gatewayState: "starting",
      }),
    ).toBe("gatewayStarting")
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: false,
        connectionState: "connecting",
        gatewayState: "running",
      }),
    ).toBe("websocketConnecting")
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: false,
        connectionState: "connected",
        gatewayState: "running",
      }),
    ).toBe("noDefaultModel")
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: true,
        connectionState: "connected",
        gatewayState: "running",
      }),
    ).toBeNull()
  })

  test("builds stable row keys and bounded size estimates", () => {
    const processRow: ChatStreamRow = {
      kind: "process",
      messages: [userMessage],
    }
    const longUserRow: ChatStreamRow = {
      kind: "message",
      message: { ...userMessage, content: "x".repeat(5000) },
    }
    const longAssistantRow: ChatStreamRow = {
      kind: "message",
      message: { ...userMessage, role: "assistant", content: "x".repeat(5000) },
    }

    expect(chatStreamRowKey(processRow)).toBe("process:user-1")
    expect(chatStreamRowKey({ kind: "working" })).toBe("working")
    expect(estimateChatStreamRow(longUserRow)).toBe(300)
    expect(estimateChatStreamRow(longAssistantRow)).toBe(1200)
    expect(estimateChatStreamRow(processRow)).toBeLessThanOrEqual(600)
  })
})
