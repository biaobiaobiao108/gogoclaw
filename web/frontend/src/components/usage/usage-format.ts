import type { UsageResponse } from "@/api/usage"

export function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}

export function formatCost(value: number): string {
  if (value === 0) return "$0.00"
  if (value < 0.0001) return "<$0.0001"
  if (value < 0.01) return `$${value.toFixed(6).replace(/0+$/, "")}`
  return `$${value.toFixed(2)}`
}

export function hasUsageActivity(data: UsageResponse): boolean {
  return (
    data.totals.calls > 0 ||
    data.totals.total_tokens > 0 ||
    data.totals.cost_nanos > 0 ||
    data.totals.cost_usd > 0 ||
    data.today.total_tokens > 0 ||
    data.daily.some((item) => item.calls > 0 || item.total_tokens > 0) ||
    data.by_model.length > 0 ||
    data.by_provider.length > 0 ||
    data.by_channel.length > 0 ||
    data.by_agent.length > 0
  )
}
