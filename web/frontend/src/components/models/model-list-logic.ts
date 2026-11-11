import type {
  ModelInfo,
  ModelProviderOption,
  ModelReferenceRename,
} from "@/api/models"

import {
  getCanonicalProviderKey,
  getProviderDefaultAPIBase,
} from "./provider-registry"

export function modelEntryAllowedInDefaultChain(model: ModelInfo) {
  return (
    !model.is_virtual &&
    model.default_model_allowed !== false &&
    model.status !== "unconfigured"
  )
}

export function sortModelList(models: ModelInfo[], defaultModelName: string) {
  return [...models].sort((a, b) => {
    const aDefault = a.model_name === defaultModelName
    const bDefault = b.model_name === defaultModelName
    if (aDefault && !bDefault) return -1
    if (!aDefault && bDefault) return 1
    if (a.available && !b.available) return -1
    if (!a.available && b.available) return 1
    return a.model_name.localeCompare(b.model_name)
  })
}

export function modelNameAllowedInDefaultChain(
  modelName: string,
  models: ModelInfo[],
) {
  const matchingModels = models.filter(
    (model) => model.model_name === modelName,
  )
  return (
    matchingModels.length > 0 &&
    matchingModels.every(modelEntryAllowedInDefaultChain)
  )
}

export function modelReferenceAllowedAfterModelChange(
  modelName: string,
  previousModels: ModelInfo[],
  models: ModelInfo[],
  providerOptions: ModelProviderOption[],
  defaultProvider: string,
) {
  const matchingModels = models.filter(
    (model) => model.model_name === modelName,
  )
  if (matchingModels.length > 0) {
    return matchingModels.every(modelEntryAllowedInDefaultChain)
  }

  if (
    previousModels.some((model) => model.model_name === modelName) ||
    modelName.trim() === ""
  ) {
    return false
  }

  const raw = modelName.trim()
  const slashIndex = raw.indexOf("/")
  const prefix = slashIndex > 0 ? raw.slice(0, slashIndex) : ""
  const explicitProvider = providerOptions.find(
    (option) =>
      getCanonicalProviderKey(option.id, providerOptions) ===
      getCanonicalProviderKey(prefix, providerOptions),
  )
  if (explicitProvider) {
    const exactLegacyMatches = models.filter(
      (model) => !model.is_virtual && model.model.trim() === raw,
    )
    if (exactLegacyMatches.length > 0) {
      const selected = selectBestRawModelMatch(
        exactLegacyMatches,
        providerOptions,
      )
      return (
        selected.default_model_allowed !== false &&
        modelInfoCanConfigureRawTemplate(selected, providerOptions)
      )
    }
  }
  const provider = explicitProvider ? prefix : defaultProvider || "openai"
  const modelId = explicitProvider ? raw.slice(slashIndex + 1).trim() : raw
  if (!modelId) return false

  const providerKey = getCanonicalProviderKey(provider, providerOptions)
  const matchingRawModels = models.filter(
    (model) =>
      !model.is_virtual &&
      getCanonicalProviderKey(model.provider, providerOptions) ===
        providerKey &&
      model.model.trim() === modelId,
  )
  if (matchingRawModels.length > 0) {
    const selected = selectBestRawModelMatch(matchingRawModels, providerOptions)
    return (
      selected.default_model_allowed !== false &&
      modelInfoCanConfigureRawTemplate(selected, providerOptions)
    )
  }

  const providerAllowed = providerOptions.some(
    (option) =>
      getCanonicalProviderKey(option.id, providerOptions) === providerKey &&
      option.default_model_allowed !== false,
  )
  return (
    providerAllowed &&
    models.some(
      (model) =>
        !model.is_virtual &&
        getCanonicalProviderKey(model.provider, providerOptions) ===
          providerKey &&
        modelInfoCanConfigureRawTemplate(model, providerOptions),
    )
  )
}

export function modelInfoCanConfigureRawTemplate(
  model: ModelInfo,
  providerOptions: ModelProviderOption[],
) {
  if (model.status !== "unconfigured") return true
  const authMethod = model.auth_method?.trim().toLowerCase()
  if (authMethod === "oauth" || authMethod === "token") return false
  if (!model.api_base?.trim()) return false
  const provider = getCanonicalProviderKey(model.provider, providerOptions)
  if (
    ["anthropic", "anthropic-messages", "alibaba-coding-anthropic"].includes(
      provider,
    )
  ) {
    return false
  }
  const normalizeBase = (value: string) =>
    value.trim().replace(/\/+$/, "").toLowerCase()
  const defaultBase = getProviderDefaultAPIBase(provider, providerOptions)
  return (
    !defaultBase || normalizeBase(model.api_base) !== normalizeBase(defaultBase)
  )
}

export function rawModelTemplateScore(
  model: ModelInfo,
  providerOptions: ModelProviderOption[],
) {
  let score = 0
  if (model.api_key?.trim()) score += 32
  const authMethod = model.auth_method?.trim().toLowerCase()
  const provider = getCanonicalProviderKey(model.provider, providerOptions)
  const normalizeBase = (value: string) =>
    value.trim().replace(/\/+$/, "").toLowerCase()
  const defaultBase = getProviderDefaultAPIBase(provider, providerOptions)
  if (
    authMethod !== "oauth" &&
    authMethod !== "token" &&
    model.api_base?.trim() &&
    (!defaultBase ||
      normalizeBase(model.api_base) !== normalizeBase(defaultBase))
  ) {
    score += 16
  }
  if (model.auth_method?.trim() || model.connect_mode?.trim()) score += 8
  if (model.enabled) score += 1
  return score
}

export function selectBestRawModelMatch(
  models: ModelInfo[],
  providerOptions: ModelProviderOption[],
) {
  return [...models].sort(
    (a, b) =>
      rawModelTemplateScore(b, providerOptions) -
        rawModelTemplateScore(a, providerOptions) || a.index - b.index,
  )[0]
}

export function modelReferenceIdentity(
  modelName: string,
  models: ModelInfo[],
  providerOptions: ModelProviderOption[],
  defaultProvider: string,
) {
  const aliasMatches = models.filter(
    (model) => model.model_name === modelName.trim(),
  )
  if (aliasMatches.length > 0) return `model_name:${modelName.trim()}`

  const raw = modelName.trim()
  if (!raw) return ""
  const slashIndex = raw.indexOf("/")
  const prefix = slashIndex > 0 ? raw.slice(0, slashIndex) : ""
  const explicitProvider = providerOptions.find(
    (option) =>
      getCanonicalProviderKey(option.id, providerOptions) ===
      getCanonicalProviderKey(prefix, providerOptions),
  )
  if (explicitProvider) {
    const exactLegacyMatches = models.filter(
      (model) => !model.is_virtual && model.model.trim() === raw,
    )
    const exactLegacyMatch = exactLegacyMatches.length
      ? selectBestRawModelMatch(exactLegacyMatches, providerOptions)
      : undefined
    if (exactLegacyMatch) {
      return `model_name:${exactLegacyMatch.model_name}`
    }
  }
  const provider = explicitProvider ? prefix : defaultProvider || "openai"
  const modelId = explicitProvider ? raw.slice(slashIndex + 1).trim() : raw
  const providerKey = getCanonicalProviderKey(provider, providerOptions)
  const configuredMatches = models.filter(
    (model) =>
      !model.is_virtual &&
      getCanonicalProviderKey(model.provider, providerOptions) ===
        providerKey &&
      model.model.trim() === modelId,
  )
  const configuredMatch = configuredMatches.length
    ? selectBestRawModelMatch(configuredMatches, providerOptions)
    : undefined
  if (configuredMatch) return `model_name:${configuredMatch.model_name}`
  return `${providerKey}/${modelId}`
}

export function applyReferenceRename(
  modelName: string,
  renames: ModelReferenceRename[],
  role: "default" | "fallback",
) {
  let renamedModelName = modelName
  for (const rename of renames) {
    if (renamedModelName === rename.from && rename[role]) {
      renamedModelName = rename.to
    }
  }
  return renamedModelName
}

export function applyReferenceRenames(
  names: string[],
  renames: ModelReferenceRename[],
) {
  const renamed: string[] = []
  for (const modelName of names) {
    const nextModelName = applyReferenceRename(modelName, renames, "fallback")
    if (!renamed.includes(nextModelName)) renamed.push(nextModelName)
  }
  return renamed
}

export function deriveModelUpdateRenames(
  updates: {
    index: number
    from: string
    stableAliases: { index: number; modelName: string }[]
  }[],
  models: ModelInfo[],
): ModelReferenceRename[] {
  const aliases = new Set(models.map((model) => model.model_name))
  return updates.flatMap((update) => {
    const updated = models.find((model) => model.index === update.index)
    const indexesStayedStable = update.stableAliases.every((stable) =>
      models.some(
        (model) =>
          model.index === stable.index && model.model_name === stable.modelName,
      ),
    )
    if (
      !updated ||
      !indexesStayedStable ||
      updated.model_name === update.from ||
      aliases.has(update.from)
    ) {
      return []
    }
    return [
      {
        from: update.from,
        to: updated.model_name,
        default: true,
        fallback: true,
      },
    ]
  })
}

export function insertByPersistedOrder(
  result: string[],
  modelName: string,
  persistedChain: string[],
) {
  const persistedIndex = persistedChain.indexOf(modelName)
  for (let index = persistedIndex + 1; index < persistedChain.length; index++) {
    const nextIndex = result.indexOf(persistedChain[index])
    if (nextIndex !== -1) {
      result.splice(nextIndex, 0, modelName)
      return
    }
  }
  for (let index = persistedIndex - 1; index >= 0; index--) {
    const previousIndex = result.indexOf(persistedChain[index])
    if (previousIndex !== -1) {
      result.splice(previousIndex + 1, 0, modelName)
      return
    }
  }
  result.push(modelName)
}

export function rebaseDefaultChainDraft(
  previousBaseline: { defaultModelName: string; fallbackChain: string[] },
  draft: { defaultModelName: string; fallbackChain: string[] },
  persisted: { defaultModelName: string; fallbackChain: string[] },
  previousModels: ModelInfo[],
  models: ModelInfo[],
  referenceRenames: ModelReferenceRename[],
  providerOptions: ModelProviderOption[],
  defaultProvider: string,
) {
  const previousDefaultModelName = applyReferenceRename(
    previousBaseline.defaultModelName,
    referenceRenames,
    "default",
  )
  const draftDefaultModelName = applyReferenceRename(
    draft.defaultModelName,
    referenceRenames,
    "default",
  )
  const defaultModelChanged = draftDefaultModelName !== previousDefaultModelName
  const keepDraftDefault =
    defaultModelChanged &&
    modelReferenceAllowedAfterModelChange(
      draftDefaultModelName,
      previousModels,
      models,
      providerOptions,
      defaultProvider,
    )
  const defaultModelName = keepDraftDefault
    ? draftDefaultModelName
    : persisted.defaultModelName
  if (!defaultModelName) {
    return { defaultModelName: "", fallbackChain: [] }
  }

  const previousFallbackChain = applyReferenceRenames(
    previousBaseline.fallbackChain,
    referenceRenames,
  )
  const draftFallbackChain = applyReferenceRenames(
    draft.fallbackChain,
    referenceRenames,
  )
  const previousBaselineNames = new Set(previousFallbackChain)
  const persistedNames = new Set(persisted.fallbackChain)
  const userRemovedNames = new Set(
    previousFallbackChain.filter(
      (modelName) => !draftFallbackChain.includes(modelName),
    ),
  )
  const seenFallbackIdentities = new Set<string>()
  const fallbackChain = draftFallbackChain.filter((modelName, index, names) => {
    const identity = modelReferenceIdentity(
      modelName,
      models,
      providerOptions,
      defaultProvider,
    )
    if (identity && seenFallbackIdentities.has(identity)) return false
    const keep =
      modelName !== defaultModelName &&
      identity !==
        modelReferenceIdentity(
          defaultModelName,
          models,
          providerOptions,
          defaultProvider,
        ) &&
      names.indexOf(modelName) === index &&
      (keepDraftDefault ||
        persistedNames.has(modelName) ||
        !previousBaselineNames.has(modelName)) &&
      modelReferenceAllowedAfterModelChange(
        modelName,
        previousModels,
        models,
        providerOptions,
        defaultProvider,
      )
    if (keep && identity) seenFallbackIdentities.add(identity)
    return keep
  })

  for (const modelName of persisted.fallbackChain) {
    const identity = modelReferenceIdentity(
      modelName,
      models,
      providerOptions,
      defaultProvider,
    )
    if (
      modelName === defaultModelName ||
      fallbackChain.includes(modelName) ||
      userRemovedNames.has(modelName) ||
      (identity && seenFallbackIdentities.has(identity))
    ) {
      continue
    }
    insertByPersistedOrder(fallbackChain, modelName, persisted.fallbackChain)
    if (identity) seenFallbackIdentities.add(identity)
  }

  return { defaultModelName, fallbackChain }
}
