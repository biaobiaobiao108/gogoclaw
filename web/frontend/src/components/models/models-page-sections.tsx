import { IconArrowsSort, IconDatabase, IconPlus } from "@tabler/icons-react"
import type { MouseEvent } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"

export function ModelsDefaultChainCard({
  defaultModelLabel,
  fallbackCount,
  onManage,
}: {
  defaultModelLabel: string
  fallbackCount: number
  onManage: () => void
}) {
  const { t } = useTranslation()

  return (
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
                  count: fallbackCount,
                })
              : t("models.defaultChain.noDefault")}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={onManage}>
          <IconArrowsSort className="size-4" />
          {t("models.defaultChain.manage")}
        </Button>
      </div>
    </div>
  )
}

export function ModelsEmptyState({
  onOpenCatalog,
  onAddModel,
}: {
  onOpenCatalog: () => void
  onAddModel: (event: MouseEvent<HTMLButtonElement>) => void
}) {
  const { t } = useTranslation()

  return (
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
          onClick={onOpenCatalog}
          className="h-8 gap-1.5 px-3 text-xs"
        >
          <IconDatabase className="size-3.5" />
          {t("models.catalog.button")}
        </Button>
        <Button
          size="sm"
          onClick={onAddModel}
          className="h-8 gap-1.5 px-3 text-xs"
        >
          <IconPlus className="size-3.5" />
          {t("models.add.button")}
        </Button>
      </div>
    </div>
  )
}
