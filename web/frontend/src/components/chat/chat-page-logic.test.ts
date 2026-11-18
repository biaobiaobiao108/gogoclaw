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
  test("prioritizes default model setup, then gateway and connection state", () => {
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: false,
        connectionState: "disconnected",
        gatewayState: "starting",
      }),
    ).toBe("noDefaultModel")
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: true,
        connectionState: "disconnected",
        gatewayState: "stopped",
      }),
    ).toBe("gatewayStopped")
    expect(
      resolveChatInputDisabledReason({
        hasDefaultModel: true,
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
    const replyRow: ChatStreamRow = {
      kind: "reply",
      id: "turn-1",
      parts: [{ kind: "process", messages: [userMessage] }],
    }
    const longUserRow: ChatStreamRow = {
      kind: "message",
      message: { ...userMessage, content: "x".repeat(5000) },
    }
    const longAssistantRow: ChatStreamRow = {
      kind: "message",
      message: { ...userMessage, role: "assistant", content: "x".repeat(5000) },
    }

    expect(chatStreamRowKey(replyRow)).toBe("reply:turn-1")
    expect(chatStreamRowKey({ kind: "working" })).toBe("working")
    expect(estimateChatStreamRow(longUserRow)).toBe(300)
    expect(estimateChatStreamRow(longAssistantRow)).toBe(1200)
    expect(estimateChatStreamRow(replyRow)).toBeLessThanOrEqual(600)
  })
})
