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
