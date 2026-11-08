import { expect, test } from "bun:test"

import type { ChatMessage } from "@/store/chat"

import { groupChatMessages } from "./group-messages"

test("groups consecutive reasoning and tool calls without crossing user or answer boundaries", () => {
  const messages: ChatMessage[] = [
    { id: "u", role: "user", content: "question", timestamp: 0 },
    {
      id: "t",
      role: "assistant",
      kind: "thought",
      content: "thinking",
      timestamp: 0,
    },
    {
      id: "c",
      role: "assistant",
      kind: "tool_calls",
      content: "",
      timestamp: 0,
    },
    { id: "a", role: "assistant", content: "answer", timestamp: 0 },
    {
      id: "c2",
      role: "assistant",
      kind: "tool_calls",
      content: "",
      timestamp: 0,
    },
  ]
  const rows = groupChatMessages(messages)
  expect(rows.map((row) => row.kind)).toEqual([
    "message",
    "process",
    "message",
    "process",
  ])
  expect(
    rows[1].kind === "process" && rows[1].messages.map((message) => message.id),
  ).toEqual(["t", "c"])
  expect(messages).toHaveLength(5)
  expect(
    groupChatMessages([
      ...messages,
      {
        id: "c3",
        role: "assistant",
        kind: "tool_calls",
        content: "",
        timestamp: 0,
      },
    ]),
  ).toHaveLength(4)
  expect(groupChatMessages([])).toEqual([])
})
