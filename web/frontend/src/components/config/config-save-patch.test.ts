import { describe, expect, test } from "bun:test"

import { buildConfigPatch } from "./config-save-patch"
import { EMPTY_FORM } from "./form-model"

function validForm() {
  return {
    ...EMPTY_FORM,
    workspace: "/workspace",
    dmScope: "per-peer",
    maxTokens: "4096",
    maxToolIterations: "15",
    toolFeedbackMaxArgsLength: "120",
    summarizeMessageThreshold: "20",
    summarizeTokenPercent: "80",
    heartbeatInterval: "30",
    cronExecTimeoutMinutes: "5",
    evolutionMinTaskCount: "3",
    evolutionMinSuccessRatio: "0.75",
    splitOnMarker: true,
  }
}

describe("buildConfigPatch", () => {
  test("validates required workspace and dmScope", () => {
    const base = validForm()
    expect(() => buildConfigPatch({ ...base, workspace: "  " }, base)).toThrow(
      "Workspace path is required.",
    )
    expect(() => buildConfigPatch({ ...base, dmScope: "" }, base)).toThrow(
      "Session scope is required.",
    )
  })

  test("serializes agent defaults including split_on_marker", () => {
    const base = validForm()
    const patch = buildConfigPatch(base, base) as {
      agents: { defaults: Record<string, unknown> }
    }
    expect(patch.agents.defaults.workspace).toBe("/workspace")
    expect(patch.agents.defaults.max_tokens).toBe(4096)
    expect(patch.agents.defaults.split_on_marker).toBe(true)
  })

  test("marks removed MCP servers as null in merge patch", () => {
    const baseline = {
      ...validForm(),
      mcpEnabled: true,
      mcpServers: [
        {
          id: "1",
          name: "alpha",
          enabled: true,
          type: "stdio" as const,
          command: "npx",
          argsText: "alpha-mcp",
          envText: "{}",
          envFile: "",
          url: "",
          headersText: "{}",
        },
        {
          id: "2",
          name: "beta",
          enabled: true,
          type: "http" as const,
          command: "",
          argsText: "",
          envText: "{}",
          envFile: "",
          url: "https://example.com/mcp",
          headersText: "{}",
        },
      ],
    }
    const next = {
      ...baseline,
      mcpServers: [baseline.mcpServers[1]],
    }

    const patch = buildConfigPatch(next, baseline) as {
      tools: { mcp: { servers: Record<string, unknown> } }
    }
    expect(patch.tools.mcp.servers.alpha).toBeNull()
    expect(patch.tools.mcp.servers.beta).toMatchObject({
      enabled: true,
      url: "https://example.com/mcp",
    })
  })
})
