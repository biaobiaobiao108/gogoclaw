import { expect, test } from "bun:test"

import type { ChatMessage } from "@/store/chat"

import { groupChatMessages } from "./group-messages"

function message(
  id: string,
  content = "",
  kind?: ChatMessage["kind"],
): ChatMessage {
  return { id, role: "assistant", timestamp: 0, content, kind }
}

test("keeps all steps of a reply in order under one stable row", () => {
  const turnId = "main-turn-eba93893-e562-45f7-a0f8-62cfc873746a"
  const first = message(`${turnId}-llm-1`, "I'll check")
  const toolCalls = message(`${first.id}-tools`, "", "tool_calls")
  const thought = message(`${turnId}-llm-2-thought`, "working", "thought")
  const last = message(`${turnId}-llm-2`, "Here's what I found")
  const user = { ...message("user", "question"), role: "user" as const }

  expect(groupChatMessages([user, first])[1]).toMatchObject({ id: turnId })
  expect(groupChatMessages([user, first, toolCalls, thought, last])).toEqual([
    { kind: "message", message: user },
    {
      kind: "reply",
      id: turnId,
      lastText: last,
      parts: [
        { kind: "text", message: first },
        { kind: "process", messages: [toolCalls, thought] },
        { kind: "text", message: last },
      ],
    },
  ])
})

test("regroups older streamed replies that recorded separate message IDs", () => {
  const first = message("ab12cd34-e562-45f7-a0f8-62cfc873746a", "interim")
  const calls = message(`${first.id}-tools`, "", "tool_calls")
  const finalId = "9111ff43-fdfa-41c8-9d66-4393d16daa4e"
  const thought = message(`${finalId}-thought`, "working", "thought")
  const last = message(finalId, "final")

  expect(groupChatMessages([first, calls, thought, last])).toEqual([
    {
      kind: "reply",
      id: first.id,
      lastText: last,
      parts: [
        { kind: "text", message: first },
        { kind: "process", messages: [calls, thought] },
        { kind: "text", message: last },
      ],
    },
  ])
})

test("keeps separately delivered messages and new user turns distinct", () => {
  const turnId = "main-turn-eba93893-e562-45f7-a0f8-62cfc873746a"
  const first = message(`${turnId}-llm-1`, "first answer")
  const scheduled = message("scheduler-1", "daily update")
  const second = message(
    "main-turn-a47d6938-136c-4bff-a6a9-2e950872e1a5-llm-1",
    "second answer",
  )
  const user = { ...message("user", "new question"), role: "user" as const }

  expect(
    groupChatMessages([first, scheduled, user, second]).map((row) => row.kind),
  ).toEqual(["reply", "reply", "message", "reply"])
  expect(groupChatMessages([])).toEqual([])
})

test("renders model split markers as paragraph breaks in Web replies", () => {
  const reply = groupChatMessages([
    message("answer", "opening<|[SPLIT]|>closing"),
  ])[0]
  expect(reply).toMatchObject({
    kind: "reply",
    parts: [{ kind: "text", message: { content: "opening\n\nclosing" } }],
  })
})

test("keeps live and saved steering in the same reply through explicit turn IDs", () => {
  const turnId = "explicit-turn"
  const opening = { ...message("opaque-opening", "opening"), turnId }
  const steering = {
    ...message("opaque-input", "change direction"),
    role: "user" as const,
    turnId,
  }
  const final = { ...message("opaque-final", "after steering"), turnId }
  expect(groupChatMessages([opening, steering, final])).toEqual([
    {
      kind: "reply",
      id: turnId,
      lastText: final,
      parts: [
        { kind: "text", message: opening },
        { kind: "steering", message: steering },
        { kind: "text", message: final },
      ],
    },
  ])
})
