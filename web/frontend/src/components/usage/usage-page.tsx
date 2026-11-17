import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import { getUsage, type UsageResponse } from "@/api/usage"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

import { UsageBreakdownTable } from "./usage-breakdown-table"
import { UsageDailyTrend } from "./usage-daily-trend"
import { hasUsageActivity } from "./usage-format"
import {
  UsageEmptyState,
  UsageErrorState,
  UsageLoadingState,
} from "./usage-states"
import { UsageSummary } from "./usage-summary"

const ranges = [7, 30, 90] as const

export function UsagePage() {
  const { t } = useTranslation()
  const [days, setDays] = useState<number>(30)
  const [data, setData] = useState<UsageResponse | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError("")
    setData(null)
    void getUsage(days)
      .then((result) => {
        if (!cancelled) setData(result)
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err))
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [days, reloadKey])

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.usage")}>
        <div className="flex items-center gap-0.5">
          {ranges.map((range) => {
            const label = t("usage.days", { count: range })
            return (
              <Button
                key={range}
                size="sm"
                variant={days === range ? "secondary" : "ghost"}
                className="h-8 min-w-8 px-2 sm:min-w-0 sm:px-2.5"
                aria-label={label}
                aria-pressed={days === range}
                onClick={() => setDays(range)}
              >
                <span className="sm:hidden">{range}</span>
                <span className="hidden sm:inline">{label}</span>
              </Button>
            )
          })}
        </div>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-4 sm:p-8">
        <div
          className="mx-auto flex max-w-6xl flex-col gap-8"
          aria-busy={loading}
        >
          {error ? (
            <UsageErrorState
              message={error}
              onRetry={() => setReloadKey((value) => value + 1)}
            />
          ) : loading ? (
            <UsageLoadingState />
          ) : data ? (
            <>
              <UsageSummary data={data} />
              {hasUsageActivity(data) ? (
                <>
                  <UsageDailyTrend daily={data.daily} />
                  <div className="grid gap-8 lg:grid-cols-2">
                    <UsageBreakdownTable
                      title={t("usage.byModel")}
                      rows={data.by_model}
                      label={(item) => item.model || item.model_id || "-"}
                    />
                    <UsageBreakdownTable
                      title={t("usage.byProvider")}
                      rows={data.by_provider}
                      label={(item) => item.provider || "-"}
                    />
                    <UsageBreakdownTable
                      title={t("usage.byChannel")}
                      rows={data.by_channel}
                      label={(item) => item.channel || "-"}
                    />
                    <UsageBreakdownTable
                      title={t("usage.byAgent")}
                      rows={data.by_agent}
                      label={(item) => item.agent_id || "-"}
                    />
                  </div>
                </>
              ) : (
                <UsageEmptyState days={days} />
              )}
            </>
          ) : null}
        </div>
      </div>
    </div>
  )
}
