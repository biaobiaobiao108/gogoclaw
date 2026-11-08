import { describe, expect, test } from "bun:test"

import { getChatState, updateChatStore } from "../../store/chat"
import { handlePicoMessage } from "./protocol"

describe("turn status", () => {
  test("tracks the active and interrupted state for the current session only", () => {
    updateChatStore({ activeTurn: undefined, interrupted: false })
    handlePicoMessage(
      {
        type: "turn.status",
        session_id: "other",
        payload: { active: true, turn_id: "wrong" },
      },
      "current",
    )
    expect(getChatState().activeTurn).toBeUndefined()
    handlePicoMessage(
      {
        type: "turn.status",
        session_id: "current",
        payload: {
          active: true,
          turn_id: "turn",
          phase: "tools",
          iteration: 2,
        },
      },
      "current",
    )
    expect(getChatState().activeTurn).toEqual({
      turnId: "turn",
      phase: "tools",
      iteration: 2,
      plan: undefined,
    })
    handlePicoMessage(
      {
        type: "turn.status",
        session_id: "current",
        payload: { active: false, interrupted: true },
      },
      "current",
    )
    expect(getChatState().activeTurn).toBeUndefined()
    expect(getChatState().interrupted).toBe(true)
  })
})

describe("approval messages", () => {
  test("tracks and clears the pending approval for the current session", () => {
    updateChatStore({ pendingApproval: undefined })
    handlePicoMessage(
      {
        type: "approval.request",
        session_id: "current",
        payload: {
          approval_id: "approval-1",
          tool: "exec",
          arguments: { command: "go test ./..." },
          risk: "Runs a command",
        },
      },
      "current",
    )
    expect(getChatState().pendingApproval).toMatchObject({
      id: "approval-1",
      tool: "exec",
      risk: "Runs a command",
    })

    handlePicoMessage(
      {
        type: "approval.resolved",
        session_id: "current",
        payload: { approval_id: "approval-1", approved: true },
      },
      "current",
    )
    expect(getChatState().pendingApproval).toBeUndefined()
  })

  test("restores approval metadata from a turn status snapshot", () => {
    updateChatStore({ pendingApproval: undefined })
    handlePicoMessage(
      {
        type: "turn.status",
        session_id: "current",
        payload: {
          active: true,
          turn_id: "turn-approval",
          phase: "awaiting_approval",
          iteration: 3,
          approval: {
            id: "approval-reconnect",
            tool: "write_file",
            risk_summary: "Changes files in the workspace.",
            created_at: "2026-11-08T08:00:00Z",
            expires_at: "2026-11-08T08:30:00Z",
          },
        },
      },
      "current",
    )
    expect(getChatState().pendingApproval).toMatchObject({
      id: "approval-reconnect",
      tool: "write_file",
      risk: "Changes files in the workspace.",
      createdAt: Date.parse("2026-11-08T08:00:00Z"),
      expiresAt: Date.parse("2026-11-08T08:30:00Z"),
    })

    handlePicoMessage(
      {
        type: "turn.status",
        session_id: "current",
        payload: { active: false },
      },
      "current",
    )
    expect(getChatState().pendingApproval).toBeUndefined()
  })

  test("clears the submitting state when approval submission fails", () => {
    updateChatStore({
      isTyping: true,
      pendingApproval: {
        id: "approval-failed",
        tool: "exec",
        isResponding: true,
      },
    })
    handlePicoMessage(
      {
        type: "error",
        session_id: "current",
        payload: {
          request_id: "approval-failed",
          message: "approval request not found",
        },
      },
      "current",
    )
    expect(getChatState().pendingApproval?.isResponding).toBe(false)
    expect(getChatState().isTyping).toBe(true)
  })
})

describe("delivery acknowledgements", () => {
  test("deduplicates repeated message.create deliveries in place", () => {
    updateChatStore({ messages: [] })
    handlePicoMessage(
      {
        type: "message.create",
        session_id: "current",
        payload: {
          message_id: "message-1",
          delivery_id: "delivery-1",
          content: "first segment",
        },
      },
      "current",
    )
    handlePicoMessage(
      {
        type: "message.create",
        session_id: "current",
        payload: {
          message_id: "message-2",
          delivery_id: "delivery-1",
          content: "first segment and final text",
        },
      },
      "current",
    )

    const messages = getChatState().messages
    expect(messages).toHaveLength(1)
    expect(messages[0]).toMatchObject({
      id: "message-1",
      deliveryId: "delivery-1",
      content: "first segment and final text",
    })
  })
})
