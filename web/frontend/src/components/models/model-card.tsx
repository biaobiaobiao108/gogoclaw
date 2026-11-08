import {
  IconArrowAutofitDown,
  IconEdit,
  IconKey,
  IconStar,
  IconStarFilled,
  IconTrash,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { ModelInfo } from "@/api/models"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

interface ModelCardProps {
  model: ModelInfo
  onEdit: (model: ModelInfo) => void
  onSetDefault: (model: ModelInfo) => void
  onToggleFallback: (model: ModelInfo) => void
  onDelete: (model: ModelInfo) => void
  inFallbackChain: boolean
  isDefault: boolean
  defaultChainAllowed: boolean
  fallbackDefaultConflict: boolean
  deleteDisabled: boolean
}

export function ModelCard({
  model,
  onEdit,
  onSetDefault,
  onToggleFallback,
  onDelete,
  inFallbackChain,
  isDefault,
  defaultChainAllowed,
  fallbackDefaultConflict,
  deleteDisabled,
}: ModelCardProps) {
  const { t } = useTranslation()
  const isOAuth = model.auth_method === "oauth"
  const status = model.status
  const statusLabel = t(`models.status.${status}`)
  const canSetDefault = model.available && !isDefault && defaultChainAllowed
  const canUseInFallback =
    model.available &&
    !isDefault &&
    !fallbackDefaultConflict &&
    defaultChainAllowed

  const setDefaultLabel = t("models.action.setDefault")
  const setDefaultDisabledReason = (() => {
    if (!model.available)
      return t("models.action.setDefaultDisabled.unavailable")
    if (isDefault) return t("models.action.setDefaultDisabled.isDefault")
    if (model.is_virtual) return t("models.action.setDefaultDisabled.isVirtual")
    if (!defaultChainAllowed) {
      return t("models.action.setDefaultDisabled.unsupportedProvider")
    }
    return setDefaultLabel
  })()

  const editLabel = t("models.action.edit")
  const toggleFallbackLabel = inFallbackChain
    ? t("models.action.removeFromFallback")
    : t("models.action.addToFallback")
  const toggleFallbackDisabledReason = (() => {
    if (!model.available)
      return t("models.action.setDefaultDisabled.unavailable")
    if (model.is_virtual) return t("models.action.setDefaultDisabled.isVirtual")
    if (!defaultChainAllowed) {
      return t("models.action.setDefaultDisabled.unsupportedProvider")
    }
    if (isDefault || fallbackDefaultConflict)
      return t("models.action.addToFallbackDisabled.isDefault")
    return toggleFallbackLabel
  })()
  const deleteLabel = t("models.action.delete")
  const deleteDisabledReason = deleteDisabled
    ? t("models.action.deleteDisabled.isDefault")
    : deleteLabel

  return (
    <div
      className={[
        "group/card relative flex w-full flex-col justify-between gap-2.5 rounded-lg border p-3.5 transition-[color,background-color,border-color,opacity,box-shadow] duration-[var(--motion-state)] ease-out",
        isDefault
          ? "border-primary/40 bg-card shadow-xs"
          : model.available
            ? "border-border/70 bg-card hover:border-border hover:bg-muted/15"
            : "border-border/40 bg-card/50 opacity-75",
      ].join(" ")}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span
            className={[
              "size-2 shrink-0 rounded-full",
              isDefault
                ? "bg-success ring-2 ring-success/20"
                : status === "available"
                  ? "bg-success/80"
                  : status === "unreachable"
                    ? "bg-warning"
                    : "bg-muted-foreground/30",
            ].join(" ")}
            title={statusLabel}
          />
          <span
            className="text-foreground truncate text-sm font-medium tracking-tight"
            title={model.model_name}
          >
            {model.model_name}
          </span>
          {isDefault && (
            <span className="bg-primary/10 text-primary shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] leading-none font-semibold">
              DEFAULT
            </span>
          )}
          {model.is_virtual && (
            <span className="bg-muted text-muted-foreground shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] leading-none font-medium">
              VIRTUAL
            </span>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-0.5 opacity-90 transition-opacity group-hover/card:opacity-100">
          {isDefault ? (
            <span
              className="text-warning p-1"
              title={t("models.badge.default")}
            >
              <IconStarFilled className="size-3.5" />
            </span>
          ) : (
            <Tooltip delayDuration={!canSetDefault ? 0 : 300}>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-warning h-7 w-7 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                  onClick={() => canSetDefault && onSetDefault(model)}
                  aria-disabled={!canSetDefault}
                  aria-label={setDefaultLabel}
                >
                  <IconStar className="size-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{setDefaultDisabledReason}</TooltipContent>
            </Tooltip>
          )}

          <Button
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-foreground h-7 w-7"
            onClick={() => onEdit(model)}
            aria-label={editLabel}
          >
            <IconEdit className="size-3.5" />
          </Button>

          <Tooltip delayDuration={canUseInFallback ? 300 : 0}>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-muted-foreground hover:text-foreground h-7 w-7 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                onClick={() => canUseInFallback && onToggleFallback(model)}
                aria-disabled={!canUseInFallback}
                aria-label={toggleFallbackLabel}
              >
                <IconArrowAutofitDown
                  className={[
                    "size-3.5",
                    inFallbackChain ? "text-primary" : "",
                  ].join(" ")}
                />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{toggleFallbackDisabledReason}</TooltipContent>
          </Tooltip>

          <Tooltip delayDuration={deleteDisabled ? 0 : 300}>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => !deleteDisabled && onDelete(model)}
                aria-disabled={deleteDisabled}
                aria-label={deleteLabel}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive h-7 w-7 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
              >
                <IconTrash className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{deleteDisabledReason}</TooltipContent>
          </Tooltip>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2 pt-2 text-xs">
        <p className="text-muted-foreground truncate font-mono text-[11px]">
          {model.model}
        </p>
        <div className="flex shrink-0 items-center gap-1.5">
          {isOAuth ? (
            <span className="text-muted-foreground bg-muted rounded px-1.5 py-0.5 font-mono text-[10px] font-medium">
              OAUTH
            </span>
          ) : status === "available" && model.api_key ? (
            <span className="text-muted-foreground/70 flex items-center gap-1 font-mono text-[10px]">
              <IconKey className="size-3" />
              {model.api_key}
            </span>
          ) : (
            <span className="text-muted-foreground/50 text-[10px]">
              {statusLabel}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
