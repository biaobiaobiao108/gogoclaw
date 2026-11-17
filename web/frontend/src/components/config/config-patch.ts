import {
  parseMultilineList,
  type TurnProfileForm,
} from "@/components/config/form-model"

export function buildStringMapMergePatch(
  next: Record<string, string>,
  previous: Record<string, string>,
): Record<string, string | null> {
  const patch: Record<string, string | null> = { ...next }
  for (const key of Object.keys(previous)) {
    if (!(key in next)) patch[key] = null
  }
  return patch
}

export function buildTurnProfilePatch(
  profile: TurnProfileForm,
): Record<string, unknown> {
  const result: Record<string, unknown> = {
    enabled: profile.enabled,
    history: { mode: profile.historyMode },
    system_prompt: { mode: profile.systemPromptMode },
    skills: { mode: profile.skillsMode },
    tools: { mode: profile.toolsMode },
  }

  if (profile.skillsMode === "custom") {
    result.skills = {
      mode: "custom",
      allow: parseMultilineList(profile.skillsAllowText),
    }
  }
  if (profile.toolsMode === "custom") {
    result.tools = {
      mode: "custom",
      allow: parseMultilineList(profile.toolsAllowText),
    }
  }

  return result
}
