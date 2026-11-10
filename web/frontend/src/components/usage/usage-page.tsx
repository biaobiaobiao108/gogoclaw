import { type ReactNode, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { getUsage, type UsageBreakdown, type UsageResponse } from "@/api/usage"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const ranges = [7, 30, 90] as const

function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}

export function formatCost(value: number): string {
  if (value === 0) return "$0.00"
  if (value < 0.0001) return "<$0.0001"
  if (value < 0.01) return `$${value.toFixed(6).replace(/0+$/, "")}`
  return `$${value.toFixed(2)}`
}

export function UsagePage() {
  const { t } = useTranslation()
  const [days, setDays] = useState<number>(30)
  const [data, setData] = useState<UsageResponse | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError("")
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
  }, [days])

  const maxDailyTokens = useMemo(
    () => Math.max(1, ...(data?.daily.map((item) => item.total_tokens) ?? [0])),
    [data],
  )

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.usage")}>
        <div className="flex items-center gap-1">
          {ranges.map((range) => (
            <Button
              key={range}
              size="sm"
              variant={days === range ? "secondary" : "ghost"}
              onClick={() => setDays(range)}
            >
              {t("usage.days", { count: range })}
            </Button>
          ))}
        </div>
      </PageHeader>

      <div className="flex-1 overflow-y-auto p-4 sm:p-8">
        <div className="mx-auto flex max-w-6xl flex-col gap-8">
          {error ? (
            <div className="text-destructive border-destructive/30 border-y py-3 text-sm">
              {error}
            </div>
          ) : null}

          <section aria-label={t("usage.summary")}>
            <div className="border-border/70 grid grid-cols-2 border-y md:grid-cols-4">
              <Metric
                label={t("usage.calls")}
                value={formatNumber(data?.totals.calls ?? 0)}
              />
              <Metric
                label={t("usage.totalTokens")}
                value={formatNumber(data?.totals.total_tokens ?? 0)}
              />
              <Metric
                label={t("usage.estimatedCost")}
                value={formatCost(data?.totals.cost_usd ?? 0)}
              />
              <Metric
                label={t("usage.todayTokens")}
                value={formatNumber(data?.today.total_tokens ?? 0)}
              />
            </div>
            {data?.governance.daily_token_budget ? (
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

          {loading && !data ? (
            <p className="text-muted-foreground text-sm">
              {t("usage.loading")}
            </p>
          ) : null}

          {data ? (
            <>
              <section>
                <SectionTitle>{t("usage.dailyTrend")}</SectionTitle>
                <div className="divide-border/50 divide-y">
                  {data.daily.map((item) => (
                    <div
                      key={item.day}
                      className="grid grid-cols-[7rem_1fr_8rem] items-center gap-3 py-2 text-sm"
                    >
                      <span className="text-muted-foreground font-mono text-xs">
                        {item.day}
                      </span>
                      <div className="bg-muted h-1.5 overflow-hidden rounded-full">
                        <div
                          className="bg-foreground/65 h-full rounded-full"
                          style={{
                            width: `${Math.max(1, (item.total_tokens / maxDailyTokens) * 100)}%`,
                          }}
                        />
                      </div>
                      <span className="text-right font-mono text-xs">
                        {formatNumber(item.total_tokens)}
                      </span>
                    </div>
                  ))}
                </div>
              </section>

              <div className="grid gap-8 lg:grid-cols-2">
                <BreakdownTable
                  title={t("usage.byModel")}
                  rows={data.by_model}
                  label={(item) => item.model || item.model_id || "-"}
                />
                <BreakdownTable
                  title={t("usage.byProvider")}
                  rows={data.by_provider}
                  label={(item) => item.provider || "-"}
                />
                <BreakdownTable
                  title={t("usage.byChannel")}
                  rows={data.by_channel}
                  label={(item) => item.channel || "-"}
                />
                <BreakdownTable
                  title={t("usage.byAgent")}
                  rows={data.by_agent}
                  label={(item) => item.agent_id || "-"}
                />
              </div>
            </>
          ) : null}
        </div>
      </div>
    </div>
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

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="text-muted-foreground mb-3 text-sm font-medium">
      {children}
    </h2>
  )
}

function BreakdownTable({
  title,
  rows,
  label,
}: {
  title: string
  rows: UsageBreakdown[]
  label: (item: UsageBreakdown) => string
}) {
  const { t } = useTranslation()
  return (
    <section>
      <SectionTitle>{title}</SectionTitle>
      {rows.length === 0 ? (
        <p className="text-muted-foreground border-border/50 border-y py-4 text-sm">
          {t("usage.empty")}
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-muted-foreground border-border/60 border-b text-left text-xs">
              <th className="py-2 font-normal">{t("usage.dimension")}</th>
              <th className="py-2 text-right font-normal">
                {t("usage.tokens")}
              </th>
              <th className="py-2 text-right font-normal">{t("usage.cost")}</th>
            </tr>
          </thead>
          <tbody className="divide-border/40 divide-y">
            {rows.slice(0, 12).map((item, index) => (
              <tr key={`${label(item)}-${index}`}>
                <td className="max-w-56 truncate py-2.5 pr-3">{label(item)}</td>
                <td className="py-2.5 text-right font-mono text-xs">
                  {formatNumber(item.total_tokens)}
                </td>
                <td className="py-2.5 text-right font-mono text-xs">
                  {formatCost(item.cost_usd)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
