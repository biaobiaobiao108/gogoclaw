import { useTranslation } from "react-i18next"

import type { UsageResponse } from "@/api/usage"

import { formatCost, formatNumber } from "./usage-format"

export function UsageSummary({ data }: { data: UsageResponse }) {
  const { t } = useTranslation()

  return (
    <section aria-label={t("usage.summary")}>
      <div className="border-border/70 grid grid-cols-2 border-y md:grid-cols-4">
        <Metric
          label={t("usage.calls")}
          value={formatNumber(data.totals.calls)}
        />
        <Metric
          label={t("usage.totalTokens")}
          value={formatNumber(data.totals.total_tokens)}
        />
        <Metric
          label={t("usage.estimatedCost")}
          value={formatCost(data.totals.cost_usd)}
        />
        <Metric
          label={t("usage.todayTokens")}
          value={formatNumber(data.today.total_tokens)}
        />
      </div>
      {data.governance.daily_token_budget ? (
        <p className="text-muted-foreground mt-3 text-xs">
          {t("usage.budgetStatus", {
            used: formatNumber(data.today.total_tokens),
            limit: formatNumber(data.governance.daily_token_budget),
            mode:
              data.governance.on_budget_exceeded === "warn"
                ? t("pages.config.budget_exceeded_warn")
                : t("pages.config.budget_exceeded_stop"),
          })}
        </p>
      ) : null}
    </section>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="border-border/60 border-r px-4 py-5 first:pl-0 last:border-r-0">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className="mt-2 text-2xl font-medium tracking-tight">{value}</div>
    </div>
  )
}
