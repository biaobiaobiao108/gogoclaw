import { IconChevronDown } from "@tabler/icons-react"
import { useState } from "react"

import type { ModelInfo } from "@/api/models"

import { ModelCard } from "./model-card"
import { ProviderIcon } from "./provider-icon"
import type { ProviderCatalogEntry } from "./provider-registry"

interface ProviderSectionProps {
  provider: Pick<ProviderCatalogEntry, "key" | "label" | "iconSlug" | "domain">
  models: ModelInfo[]
  onEdit: (model: ModelInfo) => void
  onSetDefault: (model: ModelInfo) => void
  onToggleFallback: (model: ModelInfo) => void
  onDelete: (model: ModelInfo) => void
  fallbackChain: string[]
  defaultModelName: string
  defaultModelEntryCount: number
  defaultChainAllowedModelNames: Set<string>
  fallbackDefaultConflictModelNames: Set<string>
}

export function ProviderSection({
  provider,
  models,
  onEdit,
  onSetDefault,
  onToggleFallback,
  onDelete,
  fallbackChain,
  defaultModelName,
  defaultModelEntryCount,
  defaultChainAllowedModelNames,
  fallbackDefaultConflictModelNames,
}: ProviderSectionProps) {
  const [open, setOpen] = useState(true)

  return (
    <section className="space-y-3">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="border-border/60 hover:border-border flex w-full items-center justify-between border-b pb-2 text-left transition-colors"
        aria-expanded={open}
      >
        <div className="flex items-center gap-2">
          <ProviderIcon provider={provider} />
          <span className="text-foreground text-xs font-semibold tracking-wider uppercase">
            {provider.label}
          </span>
          <span className="text-muted-foreground font-mono text-[11px]">
            ({models.length})
          </span>
        </div>
        <IconChevronDown
          className={[
            "text-muted-foreground size-4 transition-transform duration-200",
            open ? "rotate-180" : "",
          ].join(" ")}
        />
      </button>

      {open && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 md:grid-cols-3 xl:grid-cols-4">
          {models.map((model) => (
            <ModelCard
              key={model.index}
              model={model}
              onEdit={onEdit}
              onSetDefault={onSetDefault}
              onToggleFallback={onToggleFallback}
              onDelete={onDelete}
              inFallbackChain={fallbackChain.includes(model.model_name)}
              isDefault={defaultModelName === model.model_name}
              deleteDisabled={
                defaultModelName === model.model_name &&
                defaultModelEntryCount <= 1
              }
              defaultChainAllowed={defaultChainAllowedModelNames.has(
                model.model_name,
              )}
              fallbackDefaultConflict={fallbackDefaultConflictModelNames.has(
                model.model_name,
              )}
            />
          ))}
        </div>
      )}
    </section>
  )
}
