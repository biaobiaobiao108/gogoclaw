import {
  IconArrowsSort,
  IconDatabase,
  IconLoader2,
  IconPlus,
  IconStar,
} from "@tabler/icons-react"
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getModels,
  type ModelInfo,
  type ModelProviderOption,
  type ModelReferenceRename,
  updateDefaultChain,
} from "@/api/models"
import { ConfigChangeNotice } from "@/components/config-change-notice"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

import { AddModelSheet } from "./add-model-sheet"
import { CatalogDialog } from "./catalog-dialog"
import { DefaultChainDialog } from "./default-chain-dialog"
import { DeleteModelDialog } from "./delete-model-dialog"
import { EditModelSheet } from "./edit-model-sheet"
import {
  deriveModelUpdateRenames,
  modelNameAllowedInDefaultChain,
  modelReferenceIdentity,
  rebaseDefaultChainDraft,
  sortModelList,
} from "./model-list-logic"
import {
  getCanonicalProviderKey,
  getProviderCatalogMap,
} from "./provider-registry"
import type { ProviderCatalogEntry } from "./provider-registry"
import { ProviderSection } from "./provider-section"

interface ProviderGroup {
  key: string
  provider: Pick<ProviderCatalogEntry, "key" | "label" | "iconSlug" | "domain">
  models: ModelInfo[]
  hasDefault: boolean
  availableCount: number
}

export function ModelsPage() {
  const { t } = useTranslation()
  const [models, setModels] = useState<ModelInfo[]>([])
  const [providerOptions, setProviderOptions] = useState<ModelProviderOption[]>(
    [],
  )
  const [defaultProvider, setDefaultProvider] = useState("openai")
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState("")

  const [editingModel, setEditingModel] = useState<ModelInfo | null>(null)
  const [deletingModel, setDeletingModel] = useState<ModelInfo | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [catalogOpen, setCatalogOpen] = useState(false)
  const [defaultChainOpen, setDefaultChainOpen] = useState(false)
  const [defaultModelDraft, setDefaultModelDraft] = useState("")
  const [defaultModelBaseline, setDefaultModelBaseline] = useState("")
  const [fallbackChainDraft, setFallbackChainDraft] = useState<string[]>([])
  const [fallbackChainBaseline, setFallbackChainBaseline] = useState<string[]>(
    [],
  )
  const [savingChain, setSavingChain] = useState(false)
  const [modelUpdatePending, setModelUpdatePending] = useState(false)
  const refreshRequestSequence = useRef(0)
  const savingChainRef = useRef(false)
  const refreshQueuedDuringSave = useRef(false)
  const modelUpdateInFlight = useRef(0)
  const modelReconcileInFlight = useRef(false)
  const modelReconcileSequence = useRef(0)
  const pendingReferenceRenames = useRef<ModelReferenceRename[]>([])
  const pendingModelUpdates = useRef<
    {
      index: number
      from: string
      stableAliases: { index: number; modelName: string }[]
    }[]
  >([])
  const providerMap = getProviderCatalogMap(providerOptions)
  const chainDirty =
    defaultModelDraft !== defaultModelBaseline ||
    JSON.stringify(fallbackChainDraft) !== JSON.stringify(fallbackChainBaseline)
  const latestEditorState = useRef({
    defaultModelDraft,
    defaultModelBaseline,
    fallbackChainDraft,
    fallbackChainBaseline,
    models,
    providerOptions,
    defaultProvider,
    chainDirty,
  })
  useLayoutEffect(() => {
    latestEditorState.current = {
      defaultModelDraft,
      defaultModelBaseline,
      fallbackChainDraft,
      fallbackChainBaseline,
      models,
      providerOptions,
      defaultProvider,
      chainDirty,
    }
  }, [
    chainDirty,
    defaultModelBaseline,
    defaultModelDraft,
    fallbackChainBaseline,
    fallbackChainDraft,
    models,
    providerOptions,
    defaultProvider,
  ])

  const fetchModels = useCallback(async () => {
    if (modelReconcileInFlight.current) {
      return
    }
    if (savingChainRef.current || modelUpdateInFlight.current > 0) {
      refreshQueuedDuringSave.current = true
      return
    }
    const requestSequence = ++refreshRequestSequence.current
    setLoading(true)
    try {
      const data = await getModels()
      if (requestSequence !== refreshRequestSequence.current) return
      const persisted = {
        defaultModelName: data.default_model || "",
        fallbackChain: data.fallback_chain || [],
      }
      const latest = latestEditorState.current
      const renameCount = pendingReferenceRenames.current.length
      const modelUpdateCount = pendingModelUpdates.current.length
      const referenceRenames = [
        ...pendingReferenceRenames.current.slice(0, renameCount),
        ...deriveModelUpdateRenames(
          pendingModelUpdates.current.slice(0, modelUpdateCount),
          data.models,
        ),
      ]
      const rebased = latest.chainDirty
        ? rebaseDefaultChainDraft(
            {
              defaultModelName: latest.defaultModelBaseline,
              fallbackChain: latest.fallbackChainBaseline,
            },
            {
              defaultModelName: latest.defaultModelDraft,
              fallbackChain: latest.fallbackChainDraft,
            },
            persisted,
            latest.models,
            data.models,
            referenceRenames,
            data.provider_options || [],
            data.default_provider || "openai",
          )
        : persisted
      pendingReferenceRenames.current.splice(0, renameCount)
      pendingModelUpdates.current.splice(0, modelUpdateCount)
      setModels(sortModelList(data.models, rebased.defaultModelName))
      setProviderOptions(data.provider_options || [])
      setDefaultProvider(data.default_provider || "openai")
      setDefaultModelDraft(rebased.defaultModelName)
      setDefaultModelBaseline(persisted.defaultModelName)
      setFallbackChainDraft(rebased.fallbackChain)
      setFallbackChainBaseline(persisted.fallbackChain)
      setFetchError("")
    } catch (e) {
      if (requestSequence !== refreshRequestSequence.current) return
      setFetchError(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      if (requestSequence === refreshRequestSequence.current) {
        setLoading(false)
      }
    }
  }, [t])

  useEffect(() => {
    fetchModels()
  }, [fetchModels])

  const refreshAfterModelChange = useCallback(async () => {
    if (savingChainRef.current || modelUpdateInFlight.current > 0) {
      refreshQueuedDuringSave.current = true
      return
    }
    const requestSequence = ++refreshRequestSequence.current
    setLoading(true)
    try {
      const data = await getModels()
      if (requestSequence !== refreshRequestSequence.current) return
      const persisted = {
        defaultModelName: data.default_model || "",
        fallbackChain: data.fallback_chain || [],
      }
      const latest = latestEditorState.current
      const renameCount = pendingReferenceRenames.current.length
      const modelUpdateCount = pendingModelUpdates.current.length
      const referenceRenames = [
        ...pendingReferenceRenames.current.slice(0, renameCount),
        ...deriveModelUpdateRenames(
          pendingModelUpdates.current.slice(0, modelUpdateCount),
          data.models,
        ),
      ]
      const rebased = latest.chainDirty
        ? rebaseDefaultChainDraft(
            {
              defaultModelName: latest.defaultModelBaseline,
              fallbackChain: latest.fallbackChainBaseline,
            },
            {
              defaultModelName: latest.defaultModelDraft,
              fallbackChain: latest.fallbackChainDraft,
            },
            persisted,
            latest.models,
            data.models,
            referenceRenames,
            data.provider_options || [],
            data.default_provider || "openai",
          )
        : persisted
      pendingReferenceRenames.current.splice(0, renameCount)
      pendingModelUpdates.current.splice(0, modelUpdateCount)
      setModels(sortModelList(data.models, rebased.defaultModelName))
      setProviderOptions(data.provider_options || [])
      setDefaultProvider(data.default_provider || "openai")
      setDefaultModelDraft(rebased.defaultModelName)
      setDefaultModelBaseline(persisted.defaultModelName)
      setFallbackChainDraft(rebased.fallbackChain)
      setFallbackChainBaseline(persisted.fallbackChain)
      setFetchError("")
    } catch (e) {
      if (requestSequence !== refreshRequestSequence.current) return
      setFetchError(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      if (requestSequence === refreshRequestSequence.current) {
        setLoading(false)
      }
    }
  }, [t])

  const beginModelMutation = useCallback(() => {
    modelUpdateInFlight.current += 1
    ++modelReconcileSequence.current
    setModelUpdatePending(true)
    refreshQueuedDuringSave.current = true
    ++refreshRequestSequence.current
  }, [])

  const finishModelMutation = useCallback(() => {
    modelUpdateInFlight.current = Math.max(0, modelUpdateInFlight.current - 1)
    if (modelUpdateInFlight.current !== 0) return

    refreshQueuedDuringSave.current = false
    modelReconcileInFlight.current = true
    const reconcileSequence = ++modelReconcileSequence.current
    void refreshAfterModelChange().finally(() => {
      if (reconcileSequence !== modelReconcileSequence.current) return
      modelReconcileInFlight.current = false
      if (modelUpdateInFlight.current === 0) {
        setModelUpdatePending(false)
      }
    })
  }, [refreshAfterModelChange])

  useEffect(() => {
    if (savingChain || !refreshQueuedDuringSave.current) return
    refreshQueuedDuringSave.current = false
    void refreshAfterModelChange()
  }, [refreshAfterModelChange, savingChain])

  const handleSetDefault = (model: ModelInfo) => {
    if (
      !model.available ||
      !modelNameAllowedInDefaultChain(model.model_name, models)
    ) {
      return
    }
    if (defaultModelDraft === model.model_name) return
    setDefaultModelDraft(model.model_name)
    setModels((prev) => sortModelList(prev, model.model_name))
    const defaultIdentity = modelReferenceIdentity(
      model.model_name,
      models,
      providerOptions,
      defaultProvider,
    )
    setFallbackChainDraft((prev) =>
      prev.filter(
        (item) =>
          item !== model.model_name &&
          modelReferenceIdentity(
            item,
            models,
            providerOptions,
            defaultProvider,
          ) !== defaultIdentity,
      ),
    )
  }

  const handleToggleFallback = (model: ModelInfo) => {
    const defaultIdentity = modelReferenceIdentity(
      defaultModelDraft,
      models,
      providerOptions,
      defaultProvider,
    )
    if (
      model.model_name === defaultModelDraft ||
      modelReferenceIdentity(
        model.model_name,
        models,
        providerOptions,
        defaultProvider,
      ) === defaultIdentity ||
      !model.available ||
      !modelNameAllowedInDefaultChain(model.model_name, models)
    ) {
      return
    }
    setFallbackChainDraft((prev) =>
      prev.includes(model.model_name)
        ? prev.filter((item) => item !== model.model_name)
        : prev.some(
              (item) =>
                modelReferenceIdentity(
                  item,
                  models,
                  providerOptions,
                  defaultProvider,
                ) ===
                modelReferenceIdentity(
                  model.model_name,
                  models,
                  providerOptions,
                  defaultProvider,
                ),
            )
          ? prev
          : [...prev, model.model_name],
    )
  }

  const moveFallback = (modelName: string, targetIndex: number) => {
    setFallbackChainDraft((prev) => {
      const currentIndex = prev.indexOf(modelName)
      if (currentIndex === -1) {
        return prev
      }

      const next = [...prev]
      next.splice(currentIndex, 1)
      next.splice(targetIndex, 0, modelName)
      return next
    })
  }

  const handleSaveChain = async () => {
    if (modelUpdateInFlight.current > 0 || modelReconcileInFlight.current) {
      return
    }
    // Invalidate refreshes that captured the pre-save chain. Refreshes requested
    // while saving are queued and restarted after the saved draft is reconciled.
    savingChainRef.current = true
    refreshQueuedDuringSave.current = true
    ++refreshRequestSequence.current
    const submitted = {
      defaultModelName: defaultModelDraft,
      fallbackChain: fallbackChainDraft,
    }
    setSavingChain(true)
    try {
      const saved = await updateDefaultChain({
        default_model: submitted.defaultModelName,
        fallback_chain: submitted.fallbackChain,
      })
      const persisted = {
        defaultModelName: saved.default_model || "",
        fallbackChain: saved.fallback_chain || [],
      }
      const latest = latestEditorState.current
      const rebased = rebaseDefaultChainDraft(
        submitted,
        {
          defaultModelName: latest.defaultModelDraft,
          fallbackChain: latest.fallbackChainDraft,
        },
        persisted,
        latest.models,
        latest.models,
        [],
        latest.providerOptions,
        latest.defaultProvider,
      )
      setModels((current) => sortModelList(current, rebased.defaultModelName))
      setDefaultModelBaseline(persisted.defaultModelName)
      setDefaultModelDraft(rebased.defaultModelName)
      setFallbackChainBaseline(persisted.fallbackChain)
      setFallbackChainDraft(rebased.fallbackChain)
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("models.defaultChain.saveSuccess"),
        submitted.defaultModelName || t("navigation.models"),
        gateway?.restartRequired === true,
      )
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      savingChainRef.current = false
      setSavingChain(false)
    }
  }

  const handleResetChain = () => {
    setDefaultModelDraft(defaultModelBaseline)
    setModels((prev) => sortModelList(prev, defaultModelBaseline))
    setFallbackChainDraft(fallbackChainBaseline)
  }

  const grouped: Record<
    string,
    {
      provider: Pick<
        ProviderCatalogEntry,
        "key" | "label" | "iconSlug" | "domain"
      >
      models: ModelInfo[]
    }
  > = {}
  for (const model of models) {
    const providerKey = getCanonicalProviderKey(model.provider, providerOptions)
    const providerDef = providerKey ? providerMap.get(providerKey) : undefined
    if (!grouped[providerKey]) {
      grouped[providerKey] = {
        provider: {
          key: providerKey,
          label: providerDef?.label || providerKey,
          iconSlug: providerDef?.iconSlug,
          domain: providerDef?.domain,
        },
        models: [],
      }
    }
    grouped[providerKey].models.push(model)
  }

  const providerGroups: ProviderGroup[] = Object.entries(grouped)
    .map(([key, group]) => {
      const availableCount = group.models.filter(
        (model) => model.available,
      ).length
      return {
        key,
        provider: group.provider,
        models: group.models,
        hasDefault: group.models.some(
          (model) => model.model_name === defaultModelDraft,
        ),
        availableCount,
      }
    })
    .sort((a, b) => {
      if (a.hasDefault && !b.hasDefault) return -1
      if (!a.hasDefault && b.hasDefault) return 1

      if (a.availableCount !== b.availableCount) {
        return b.availableCount - a.availableCount
      }

      const aPriority = -(providerMap.get(a.key)?.priority ?? 0)
      const bPriority = -(providerMap.get(b.key)?.priority ?? 0)
      if (aPriority !== bPriority) {
        return aPriority - bPriority
      }

      return a.provider.label.localeCompare(b.provider.label)
    })

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
  const defaultModelLabel = defaultModel?.model_name ?? defaultModelDraft
  const defaultChainAllowedModelNames = new Set(
    models
      .filter((model) =>
        modelNameAllowedInDefaultChain(model.model_name, models),
      )
      .map((model) => model.model_name),
  )
  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.models")}>
        <div className="flex items-center gap-3">
          <Button
            size="sm"
            variant="outline"
            onClick={() => setDefaultChainOpen(true)}
            aria-label={t("models.defaultChain.button")}
            disabled={
              savingChain ||
              modelUpdatePending ||
              (models.length === 0 && !defaultModelDraft)
            }
          >
            <IconArrowsSort className="size-4" />
            <span className="hidden sm:inline">
              {t("models.defaultChain.button")}
            </span>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setCatalogOpen(true)}
            aria-label={t("models.catalog.button")}
            disabled={
              savingChain || modelUpdatePending || providerOptions.length === 0
            }
          >
            <IconDatabase className="size-4" />
            <span className="hidden sm:inline">
              {t("models.catalog.button")}
            </span>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setAddOpen(true)}
            aria-label={t("models.add.button")}
            disabled={
              savingChain || modelUpdatePending || providerOptions.length === 0
            }
          >
            <IconPlus className="size-4" />
            <span className="hidden sm:inline">{t("models.add.button")}</span>
          </Button>
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-6">
        <div className="pt-2">
          {!defaultModelDraft && (
            <div className="text-muted-foreground flex items-center gap-1.5 text-sm">
              <span>{t("models.noDefaultHintPrefix")}</span>
              <IconStar className="size-3.5 shrink-0" />
              <span>{t("models.noDefaultHintSuffix")}</span>
            </div>
          )}
          <p className="text-muted-foreground mt-1 text-sm">
            {t("models.description")}
          </p>
          {!loading && (models.length > 0 || defaultModelDraft) && (
            <div className="bg-muted/30 mt-4 rounded-xl border px-4 py-3">
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div>
                  <p className="text-sm font-medium">
                    {t("models.defaultChain.title")}
                  </p>
                  <p className="text-muted-foreground mt-1 text-sm">
                    {defaultModelLabel
                      ? t("models.defaultChain.summary", {
                          model: defaultModelLabel,
                          count: fallbackChainDraft.length,
                        })
                      : t("models.defaultChain.noDefault")}
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setDefaultChainOpen(true)}
                >
                  <IconArrowsSort className="size-4" />
                  {t("models.defaultChain.manage")}
                </Button>
              </div>
            </div>
          )}
          {!loading && providerOptions.length === 0 && (
            <p className="text-muted-foreground mt-1 text-sm">
              {t("models.providerCatalogUnavailable")}
            </p>
          )}
        </div>

        {(loading || savingChain || modelUpdatePending) && (
          <div className="flex items-center justify-center py-20">
            <IconLoader2 className="text-muted-foreground size-6 animate-spin" />
          </div>
        )}

        {fetchError && (
          <div className="bg-destructive/10 rounded-lg px-4 py-3 text-sm">
            <p className="text-destructive">{fetchError}</p>
            <div className="mt-3 flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  void fetchModels()
                }}
              >
                {t("models.retry")}
              </Button>
            </div>
          </div>
        )}

        {!loading &&
          !savingChain &&
          !modelUpdatePending &&
          !fetchError &&
          providerGroups.length === 0 && (
            <div className="flex flex-col items-center justify-center py-20 text-center">
              <div className="border-border/70 bg-muted/40 text-muted-foreground mb-4 flex size-12 items-center justify-center rounded-lg border">
                <IconDatabase className="size-6 opacity-70" />
              </div>
              <h2 className="text-foreground text-sm font-semibold tracking-tight">
                {t("models.empty.title")}
              </h2>
              <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
                {t("models.empty.description")}
              </p>
              <div className="mt-4 flex items-center gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setCatalogOpen(true)}
                  className="h-8 gap-1.5 px-3 text-xs"
                >
                  <IconDatabase className="size-3.5" />
                  {t("models.catalog.button")}
                </Button>
                <Button
                  size="sm"
                  onClick={() => setAddOpen(true)}
                  className="h-8 gap-1.5 px-3 text-xs"
                >
                  <IconPlus className="size-3.5" />
                  {t("models.add.button")}
                </Button>
              </div>
            </div>
          )}

        {!loading &&
          !savingChain &&
          !modelUpdatePending &&
          !fetchError &&
          providerGroups.length > 0 && (
            <div className="space-y-6 pb-8">
              {providerGroups.map((providerGroup) => (
                <ProviderSection
                  key={providerGroup.key}
                  provider={providerGroup.provider}
                  models={providerGroup.models}
                  onEdit={setEditingModel}
                  onSetDefault={handleSetDefault}
                  onToggleFallback={handleToggleFallback}
                  onDelete={setDeletingModel}
                  fallbackChain={fallbackChainDraft}
                  defaultModelName={defaultModelDraft}
                  defaultModelEntryCount={defaultModelEntryCount}
                  defaultChainAllowedModelNames={defaultChainAllowedModelNames}
                  fallbackDefaultConflictModelNames={
                    fallbackDefaultConflictModelNames
                  }
                />
              ))}
            </div>
          )}
      </div>

      <EditModelSheet
        model={editingModel}
        open={editingModel !== null}
        onClose={() => setEditingModel(null)}
        onUpdateStarted={() => {
          beginModelMutation()
          if (
            editingModel &&
            !pendingModelUpdates.current.some(
              (update) => update.index === editingModel.index,
            )
          ) {
            pendingModelUpdates.current.push({
              index: editingModel.index,
              from: editingModel.model_name,
              stableAliases: models
                .filter((model) => model.index !== editingModel.index)
                .map((model) => ({
                  index: model.index,
                  modelName: model.model_name,
                })),
            })
          }
        }}
        onUpdated={(referenceRename) => {
          if (referenceRename) {
            pendingReferenceRenames.current.push(referenceRename)
          }
          void refreshAfterModelChange()
        }}
        onSaved={() => {
          void refreshAfterModelChange()
        }}
        onUpdateSettled={finishModelMutation}
        providerOptions={providerOptions}
      />

      <AddModelSheet
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSaved={() => {
          void refreshAfterModelChange()
        }}
        onMutationStarted={beginModelMutation}
        onMutationSettled={finishModelMutation}
        existingModelNames={models.map((model) => model.model_name)}
        hasDefaultModel={defaultModelDraft !== ""}
        providerOptions={providerOptions}
      />

      <DeleteModelDialog
        model={deletingModel}
        isDefaultModel={
          deletingModel?.model_name === defaultModelDraft &&
          defaultModelEntryCount <= 1
        }
        onClose={() => setDeletingModel(null)}
        onDeleted={() => {
          void refreshAfterModelChange()
        }}
        onMutationStarted={beginModelMutation}
        onMutationSettled={finishModelMutation}
      />

      <CatalogDialog
        open={catalogOpen}
        onClose={() => setCatalogOpen(false)}
        onModelAdded={() => {
          void refreshAfterModelChange()
        }}
        onMutationStarted={beginModelMutation}
        onMutationSettled={finishModelMutation}
        providerOptions={providerOptions}
      />

      <DefaultChainDialog
        open={defaultChainOpen}
        onClose={() => setDefaultChainOpen(false)}
        models={models}
        defaultModelName={defaultModelDraft}
        fallbackChain={fallbackChainDraft}
        onMoveUp={(modelName) => {
          const index = fallbackChainDraft.indexOf(modelName)
          if (index > 0) {
            moveFallback(modelName, index - 1)
          }
        }}
        onMoveDown={(modelName) => {
          const index = fallbackChainDraft.indexOf(modelName)
          if (index !== -1 && index < fallbackChainDraft.length - 1) {
            moveFallback(modelName, index + 1)
          }
        }}
        onMoveTop={(modelName) => moveFallback(modelName, 0)}
        onMoveBottom={(modelName) =>
          moveFallback(modelName, Math.max(fallbackChainDraft.length - 1, 0))
        }
        onRemove={(modelName) =>
          setFallbackChainDraft((prev) =>
            prev.filter((item) => item !== modelName),
          )
        }
      />

      {chainDirty && (
        <div className="border-border/70 bg-background/95 supports-backdrop-filter:bg-background/80 shrink-0 border-t px-4 py-3 shadow-[0_-12px_30px_rgba(15,23,42,0.10)] backdrop-blur">
          <div className="mx-auto flex w-full max-w-[1400px] flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex-1">
              <ConfigChangeNotice
                kind="save"
                title={t("common.saveChangesTitle")}
                description={t("models.defaultChain.unsaved")}
              />
            </div>
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="outline"
                onClick={handleResetChain}
                disabled={savingChain || modelUpdatePending}
              >
                {t("common.reset")}
              </Button>
              <Button
                onClick={handleSaveChain}
                disabled={savingChain || modelUpdatePending}
              >
                {savingChain ? t("common.saving") : t("common.save")}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
