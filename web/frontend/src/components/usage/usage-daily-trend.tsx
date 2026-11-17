import type { ReactNode } from "react"
import { useMemo } from "react"
import { useTranslation } from "react-i18next"

import type { UsageResponse } from "@/api/usage"

import { formatNumber } from "./usage-format"

export function UsageDailyTrend({ daily }: { daily: UsageResponse["daily"] }) {
  const { t } = useTranslation()
  const maxDailyTokens = useMemo(
    () => Math.max(1, ...daily.map((item) => item.total_tokens)),
    [daily],
  )

  return (
    <section>
      <SectionTitle>{t("usage.dailyTrend")}</SectionTitle>
      {daily.length === 0 ? (
        <p className="text-muted-foreground border-border/50 border-y py-4 text-sm">
          {t("usage.empty")}
        </p>
      ) : (
        <div
          role="list"
          aria-label={t("usage.dailyTrend")}
          className="divide-border/50 divide-y"
        >
          {daily.map((item) => (
            <div
              key={item.day}
              role="listitem"
              className="grid grid-cols-[minmax(0,6.5rem)_1fr_minmax(0,5rem)] items-center gap-3 py-2 text-sm"
              aria-label={t("usage.trendRow", {
                day: item.day,
                tokens: formatNumber(item.total_tokens),
              })}
            >
              <span className="text-muted-foreground truncate font-mono text-xs">
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
      )}
    </section>
  )
}

export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="text-muted-foreground mb-3 text-sm font-medium">
      {children}
    </h2>
  )
}
