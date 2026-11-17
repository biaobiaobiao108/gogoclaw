import { useTranslation } from "react-i18next"

import type { UsageBreakdown } from "@/api/usage"

import { SectionTitle } from "./usage-daily-trend"
import { formatCost, formatNumber } from "./usage-format"

export function UsageBreakdownTable({
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
