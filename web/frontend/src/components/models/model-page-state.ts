import type { ModelInfo, ModelProviderOption } from "@/api/models"

import {
  modelNameAllowedInDefaultChain,
  modelReferenceIdentity,
} from "./model-list-logic"
import {
  getCanonicalProviderKey,
  type ProviderCatalogEntry,
} from "./provider-registry"

export interface ProviderGroup {
  key: string
  provider: Pick<ProviderCatalogEntry, "key" | "label" | "iconSlug" | "domain">
  models: ModelInfo[]
  hasDefault: boolean
  availableCount: number
}

export function groupModelsByProvider(
  models: ModelInfo[],
  defaultModelDraft: string,
  providerOptions: ModelProviderOption[],
  providerMap: Map<string, ProviderCatalogEntry>,
): ProviderGroup[] {
  const grouped = new Map<
    string,
    Omit<ProviderGroup, "hasDefault" | "availableCount">
  >()
  for (const model of models) {
    const providerKey = getCanonicalProviderKey(model.provider, providerOptions)
    const providerDef = providerKey ? providerMap.get(providerKey) : undefined
    let group = grouped.get(providerKey)
    if (!group) {
      group = {
        key: providerKey,
        provider: {
          key: providerKey,
          label: providerDef?.label || providerKey,
          iconSlug: providerDef?.iconSlug,
          domain: providerDef?.domain,
        },
        models: [],
      }
      grouped.set(providerKey, group)
    }
    group.models.push(model)
  }

  return [...grouped.values()]
    .map((group) => ({
      ...group,
      hasDefault: group.models.some(
        (model) => model.model_name === defaultModelDraft,
      ),
      availableCount: group.models.filter((model) => model.available).length,
    }))
    .sort((a, b) => {
      if (a.hasDefault && !b.hasDefault) return -1
      if (!a.hasDefault && b.hasDefault) return 1
      if (a.availableCount !== b.availableCount) {
        return b.availableCount - a.availableCount
      }
      const aPriority = -(providerMap.get(a.key)?.priority ?? 0)
      const bPriority = -(providerMap.get(b.key)?.priority ?? 0)
      if (aPriority !== bPriority) return aPriority - bPriority
      return a.provider.label.localeCompare(b.provider.label)
    })
}

export function deriveDefaultChainState({
  models,
  defaultProvider,
  providerOptions,
  defaultModelDraft,
  fallbackChainDraft,
}: {
  models: ModelInfo[]
  defaultProvider: string
  providerOptions: ModelProviderOption[]
  defaultModelDraft: string
  fallbackChainDraft: string[]
}) {
  const defaultModel = models.find(
    (model) => model.model_name === defaultModelDraft,
  )
  const defaultModelEntryCount = models.filter(
    (model) => model.model_name === defaultModelDraft,
  ).length
  const defaultModelIdentity = modelReferenceIdentity(
    defaultModelDraft,
    models,
    providerOptions,
    defaultProvider,
  )
  const fallbackIdentities = new Set(
    fallbackChainDraft.map((modelName) =>
      modelReferenceIdentity(
        modelName,
        models,
        providerOptions,
        defaultProvider,
      ),
    ),
  )
  const fallbackDefaultConflictModelNames = new Set(
    models
      .filter((model) => {
        const identity = modelReferenceIdentity(
          model.model_name,
          models,
          providerOptions,
          defaultProvider,
        )
        return (
          identity === defaultModelIdentity ||
          (!fallbackChainDraft.includes(model.model_name) &&
            fallbackIdentities.has(identity))
        )
      })
      .map((model) => model.model_name),
  )
  const defaultChainAllowedModelNames = new Set(
    models
      .filter((model) =>
        modelNameAllowedInDefaultChain(model.model_name, models),
      )
      .map((model) => model.model_name),
  )

  return {
    defaultModel,
    defaultModelEntryCount,
    defaultModelIdentity,
    fallbackIdentities,
    fallbackDefaultConflictModelNames,
    defaultModelLabel: defaultModel?.model_name ?? defaultModelDraft,
    defaultChainAllowedModelNames,
  }
}
