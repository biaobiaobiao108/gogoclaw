import {
  buildStringMapMergePatch,
  buildTurnProfilePatch,
} from "@/components/config/config-patch"
import type { CoreConfigForm } from "@/components/config/form-model"
import {
  parseFloatField,
  parseIntField,
  parseJSONObjectField,
  parseMultilineList,
} from "@/components/config/form-model"

export function buildConfigPatch(
  form: CoreConfigForm,
  baseline: CoreConfigForm,
): Record<string, unknown> {
  const workspace = form.workspace.trim()
  const dmScope = form.dmScope.trim()

  if (!workspace) {
    throw new Error("Workspace path is required.")
  }
  if (!dmScope) {
    throw new Error("Session scope is required.")
  }

  const maxTokens = parseIntField(form.maxTokens, "Max tokens", {
    min: 1,
  })
  const contextWindow = form.contextWindow.trim()
    ? parseIntField(form.contextWindow, "Context window", { min: 1 })
    : undefined
  const maxToolIterations = parseIntField(
    form.maxToolIterations,
    "Max tool iterations",
    { min: 1 },
  )
  const toolFeedbackMaxArgsLength = parseIntField(
    form.toolFeedbackMaxArgsLength,
    "Tool feedback max args length",
    { min: 0 },
  )
  const summarizeMessageThreshold = parseIntField(
    form.summarizeMessageThreshold,
    "Summarize message threshold",
    { min: 1 },
  )
  const summarizeTokenPercent = parseIntField(
    form.summarizeTokenPercent,
    "Summarize token percent",
    { min: 1, max: 100 },
  )
  const sessionTokenBudget = form.sessionTokenBudget.trim()
    ? parseIntField(form.sessionTokenBudget, "Session token budget", {
        min: 0,
      })
    : 0
  const dailyTokenBudget = form.dailyTokenBudget.trim()
    ? parseIntField(form.dailyTokenBudget, "Daily token budget", {
        min: 0,
      })
    : 0
  const turnProfile = buildTurnProfilePatch(form.turnProfile)
  const heartbeatInterval = parseIntField(
    form.heartbeatInterval,
    "Heartbeat interval",
    { min: 1 },
  )
  const cronExecTimeoutMinutes = parseIntField(
    form.cronExecTimeoutMinutes,
    "Cron exec timeout",
    { min: 0 },
  )
  const evolutionMinTaskCount = parseIntField(
    form.evolutionMinTaskCount,
    "Evolution minimum task count",
    { min: 1 },
  )
  const evolutionMinSuccessRatio = parseFloatField(
    form.evolutionMinSuccessRatio,
    "Evolution minimum success ratio",
    { min: 0.01, max: 1 },
  )
  const execConfigPatch: Record<string, unknown> = {
    enabled: form.execEnabled,
  }

  let mcpServersPatch: Record<string, Record<string, unknown> | null> = {}
  if (form.mcpEnabled) {
    const baselineServerNames = new Set(
      baseline.mcpServers
        .map((server) => server.name.trim())
        .filter((name) => name !== ""),
    )

    const normalizedServers = form.mcpServers
      .map((server) => ({
        ...server,
        name: server.name.trim(),
        url: server.url.trim(),
        command: server.command.trim(),
        envFile: server.envFile.trim(),
      }))
      .filter((server) => server.name !== "")

    const serverNameCounts = new Map<string, number>()
    for (const server of normalizedServers) {
      serverNameCounts.set(
        server.name,
        (serverNameCounts.get(server.name) ?? 0) + 1,
      )
    }

    const duplicateNames = Array.from(serverNameCounts.entries())
      .filter(([, count]) => count > 1)
      .map(([name]) => name)
      .sort((a, b) => a.localeCompare(b))

    if (duplicateNames.length > 0) {
      throw new Error(
        `MCP server names must be unique. Duplicates: ${duplicateNames.join(", ")}.`,
      )
    }

    const currentServerNames = new Set(
      normalizedServers.map((server) => server.name),
    )

    const removedServerEntries = Array.from(baselineServerNames)
      .filter((name) => !currentServerNames.has(name))
      .map((name) => [name, null] as const)

    const baselineServersByName = new Map(
      baseline.mcpServers
        .map((server) => ({
          ...server,
          name: server.name.trim(),
        }))
        .filter((server) => server.name !== "")
        .map((server) => [server.name, server] as const),
    )

    const upsertServerEntries = normalizedServers.map((server) => {
      const baselineServer = baselineServersByName.get(server.name)
      const shouldValidateServer = server.enabled

      if (server.type !== "stdio") {
        if (shouldValidateServer && server.url === "") {
          throw new Error(`MCP server ${server.name} requires a URL.`)
        }

        if (shouldValidateServer) {
          try {
            const parsedURL = new URL(server.url)
            if (
              parsedURL.protocol !== "http:" &&
              parsedURL.protocol !== "https:"
            ) {
              throw new Error("invalid protocol")
            }
          } catch {
            throw new Error(
              `MCP server ${server.name} requires a valid HTTP(S) URL.`,
            )
          }
        }

        const baselineHeaders = baselineServer
          ? parseJSONObjectField(
              baselineServer.headersText,
              `Saved MCP server ${server.name} headers`,
            )
          : {}

        return [
          server.name,
          {
            enabled: server.enabled,
            type: server.type,
            url: server.url,
            headers: buildStringMapMergePatch(
              shouldValidateServer
                ? parseJSONObjectField(
                    server.headersText,
                    `MCP server ${server.name} headers`,
                  )
                : baselineHeaders,
              baselineHeaders,
            ),
            command: null,
            args: null,
            env: null,
            env_file: null,
          },
        ] as const
      }

      if (shouldValidateServer && server.command === "") {
        throw new Error(`MCP server ${server.name} requires a command.`)
      }

      const baselineEnv = baselineServer
        ? parseJSONObjectField(
            baselineServer.envText,
            `Saved MCP server ${server.name} env`,
          )
        : {}

      return [
        server.name,
        {
          enabled: server.enabled,
          type: "stdio",
          command: server.command,
          args: parseMultilineList(server.argsText),
          env: buildStringMapMergePatch(
            shouldValidateServer
              ? parseJSONObjectField(
                  server.envText,
                  `MCP server ${server.name} env`,
                )
              : baselineEnv,
            baselineEnv,
          ),
          env_file: server.envFile === "" ? null : server.envFile,
          url: null,
          headers: null,
        },
      ] as const
    })

    mcpServersPatch = Object.fromEntries([
      ...upsertServerEntries,
      ...removedServerEntries,
    ])
  }

  if (form.execEnabled) {
    execConfigPatch.allow_remote = form.allowRemote
    execConfigPatch.enable_deny_patterns = form.enableDenyPatterns
    execConfigPatch.custom_allow_patterns = parseMultilineList(
      form.customAllowPatternsText,
    )
    execConfigPatch.timeout_seconds = parseIntField(
      form.execTimeoutSeconds,
      "Exec timeout",
      { min: 0 },
    )

    if (form.enableDenyPatterns) {
      execConfigPatch.custom_deny_patterns = parseMultilineList(
        form.customDenyPatternsText,
      )
    }
  }

  return {
    agents: {
      defaults: {
        workspace,
        restrict_to_workspace: form.restrictToWorkspace,
        split_on_marker: form.splitOnMarker,
        tool_feedback: {
          enabled: form.toolFeedbackEnabled,
          max_args_length: toolFeedbackMaxArgsLength,
          separate_messages: form.toolFeedbackSeparateMessages,
        },
        max_tokens: maxTokens,
        context_window: contextWindow,
        max_tool_iterations: maxToolIterations,
        summarize_message_threshold: summarizeMessageThreshold,
        summarize_token_percent: summarizeTokenPercent,
        usage_governance: {
          session_token_budget: sessionTokenBudget,
          daily_token_budget: dailyTokenBudget,
          on_budget_exceeded: form.budgetExceededMode,
        },
        turn_profile: turnProfile,
      },
    },
    session: {
      dm_scope: dmScope,
    },
    evolution: {
      enabled: form.evolutionEnabled,
      mode: form.evolutionMode,
      state_dir:
        form.evolutionStateDir.trim() === ""
          ? null
          : form.evolutionStateDir.trim(),
      min_task_count: evolutionMinTaskCount,
      min_success_ratio: evolutionMinSuccessRatio,
      cold_path_trigger: form.evolutionColdPathTrigger,
      cold_path_times: parseMultilineList(form.evolutionColdPathTimesText),
    },
    tools: {
      approval: { enabled: form.approvalEnabled, yolo: form.approvalYolo },
      cron: {
        allow_command: form.allowCommand,
        exec_timeout_minutes: cronExecTimeoutMinutes,
      },
      exec: execConfigPatch,
      mcp: {
        enabled: form.mcpEnabled,
        servers: mcpServersPatch,
      },
    },
    heartbeat: {
      enabled: form.heartbeatEnabled,
      interval: heartbeatInterval,
    },
    devices: {
      enabled: form.devicesEnabled,
      monitor_usb: form.monitorUSB,
    },
  }
}
