import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

export function UsageLoadingState() {
  const { t } = useTranslation()

  return (
    <div role="status" aria-live="polite" className="space-y-8">
      <span className="sr-only">{t("usage.loading")}</span>
      <section aria-hidden="true">
        <div className="border-border/70 grid grid-cols-2 border-y md:grid-cols-4">
          {[0, 1, 2, 3].map((index) => (
            <div
              key={index}
              className="border-border/60 border-r px-4 py-5 first:pl-0 last:border-r-0"
            >
              <Skeleton className="h-3 w-20" />
              <Skeleton className="mt-3 h-7 w-24" />
            </div>
          ))}
        </div>
      </section>
      <section aria-hidden="true" className="space-y-3">
        <Skeleton className="h-4 w-24" />
        {[0, 1, 2, 3, 4].map((index) => (
          <Skeleton key={index} className="h-6 w-full" />
        ))}
      </section>
      <div aria-hidden="true" className="grid gap-8 lg:grid-cols-2">
        {[0, 1, 2, 3].map((index) => (
          <section key={index} className="space-y-3">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-32 w-full" />
          </section>
        ))}
      </div>
    </div>
  )
}

export function UsageErrorState({
  message,
  onRetry,
}: {
  message: string
  onRetry: () => void
}) {
  const { t } = useTranslation()

  return (
    <section role="alert" className="border-destructive/30 border-y px-4 py-5">
      <h2 className="text-destructive text-sm font-medium">
        {t("usage.loadError")}
      </h2>
      <p className="text-muted-foreground mt-1 text-sm break-words">
        {message}
      </p>
      <Button variant="outline" size="sm" className="mt-4" onClick={onRetry}>
        {t("usage.retry")}
      </Button>
    </section>
  )
}

export function UsageEmptyState({ days }: { days: number }) {
  const { t } = useTranslation()

  return (
    <section className="border-border/60 border-y py-10">
      <h2 className="text-sm font-medium">{t("usage.emptyTitle")}</h2>
      <p className="text-muted-foreground mt-2 max-w-xl text-sm leading-relaxed">
        {t("usage.emptyDescription", { count: days })}
      </p>
    </section>
  )
}
