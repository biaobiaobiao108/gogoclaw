import { describe, expect, test } from "bun:test"

import { buildStringMapMergePatch, buildTurnProfilePatch } from "./config-patch"
import type { TurnProfileForm } from "./form-model"

describe("buildStringMapMergePatch", () => {
  test("marks removed keys as null while preserving updated and added keys", () => {
    expect(
      buildStringMapMergePatch(
        { KEEP: "new", ADD: "1" },
        { KEEP: "old", REMOVE: "gone" },
      ),
    ).toEqual({
      KEEP: "new",
      ADD: "1",
      REMOVE: null,
    })
  })
})

describe("buildTurnProfilePatch", () => {
  const baseProfile: TurnProfileForm = {
    enabled: true,
    historyMode: "off",
    systemPromptMode: "default",
    skillsMode: "default",
    skillsAllowText: "alpha\nbeta",
    toolsMode: "default",
    toolsAllowText: "read_file\nexec",
  }

  test("omits allow lists when skillsMode and toolsMode are not custom", () => {
    expect(buildTurnProfilePatch(baseProfile)).toEqual({
      enabled: true,
      history: { mode: "off" },
      system_prompt: { mode: "default" },
      skills: { mode: "default" },
      tools: { mode: "default" },
    })
  })

  test("includes parsed allow lists when custom mode is selected", () => {
    expect(
      buildTurnProfilePatch({
        ...baseProfile,
        skillsMode: "custom",
        toolsMode: "custom",
      }),
    ).toEqual({
      enabled: true,
      history: { mode: "off" },
      system_prompt: { mode: "default" },
      skills: { mode: "custom", allow: ["alpha", "beta"] },
      tools: { mode: "custom", allow: ["read_file", "exec"] },
    })
  })
})
