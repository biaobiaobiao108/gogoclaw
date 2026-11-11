import { describe, expect, test } from "bun:test"

import {
  buildFormFromConfig,
  EMPTY_FORM,
  parseCIDRText,
  parseFloatField,
  parseIntField,
  parseJSONObjectField,
  parseMultilineList,
} from "./form-model"

describe("form field parsing", () => {
  test("parses integer fields and enforces bounds", () => {
    expect(parseIntField("12", "Count", { min: 1, max: 20 })).toBe(12)
    expect(() => parseIntField("1.5", "Count")).toThrow(
      "Count must be an integer.",
    )
    expect(() => parseIntField("0", "Count", { min: 1 })).toThrow(
      "Count must be >= 1.",
    )
    expect(() => parseIntField("21", "Count", { max: 20 })).toThrow(
      "Count must be <= 20.",
    )
  })

  test("parses float fields and rejects non-finite values", () => {
    expect(parseFloatField("0.75", "Ratio", { min: 0.01, max: 1 })).toBe(0.75)
    expect(() => parseFloatField("NaN", "Ratio")).toThrow(
      "Ratio must be a number.",
    )
    expect(() => parseFloatField("1.1", "Ratio", { max: 1 })).toThrow(
      "Ratio must be <= 1.",
    )
  })

  test("parses newline and comma separated lists without blank entries", () => {
    expect(parseCIDRText(" 10.0.0.0/8, 192.168.0.0/16\n\n")).toEqual([
      "10.0.0.0/8",
      "192.168.0.0/16",
    ])
    expect(parseMultilineList(" alpha \n\n beta \n")).toEqual(["alpha", "beta"])
    expect(parseCIDRText("   ")).toEqual([])
  })

  test("parses string maps and reports invalid object shapes", () => {
    expect(parseJSONObjectField('{"A":"1","B":"2"}', "Headers")).toEqual({
      A: "1",
      B: "2",
    })
    expect(() => parseJSONObjectField("{invalid", "Headers")).toThrow(
      "Headers must be valid JSON.",
    )
    expect(() => parseJSONObjectField('["A"]', "Headers")).toThrow(
      "Headers must be a JSON object.",
    )
    expect(() => parseJSONObjectField('{"A":1}', "Headers")).toThrow(
      "Headers.A must be a string.",
    )
  })
})

describe("buildFormFromConfig", () => {
  test("maps nested config values and preserves unknown fields through defaults", () => {
    const form = buildFormFromConfig({
      agents: {
        defaults: {
          workspace: "/srv/gogoclaw",
          max_tokens: 8192,
          usage_governance: {
            daily_token_budget: 500,
            on_budget_exceeded: "warn",
          },
          turn_profile: {
            enabled: true,
            skills: { mode: "custom", allow: ["search", "read"] },
          },
        },
      },
      tools: {
        mcp: {
          enabled: true,
          servers: {
            filesystem: {
              command: "npx",
              args: ["-y", "server-filesystem"],
              env: { ROOT: "/srv" },
            },
          },
        },
      },
    })

    expect(form.workspace).toBe("/srv/gogoclaw")
    expect(form.maxTokens).toBe("8192")
    expect(form.dailyTokenBudget).toBe("500")
    expect(form.budgetExceededMode).toBe("warn")
    expect(form.turnProfile).toMatchObject({
      enabled: true,
      skillsMode: "custom",
      skillsAllowText: "search\nread",
    })
    expect(form.mcpServers).toHaveLength(1)
    expect(form.mcpServers[0]).toMatchObject({
      name: "filesystem",
      type: "stdio",
      command: "npx",
      argsText: "-y\nserver-filesystem",
      envText: '{\n  "ROOT": "/srv"\n}',
    })
    expect(form.heartbeatEnabled).toBe(EMPTY_FORM.heartbeatEnabled)
  })
})
