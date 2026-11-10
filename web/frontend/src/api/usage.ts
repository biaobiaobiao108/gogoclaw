import { launcherFetch } from "@/api/http"

export interface UsageTotals {
  calls: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cost_nanos: number
  cost_usd: number
}

export interface UsageBreakdown extends UsageTotals {
  provider?: string
  model?: string
  model_id?: string
  channel?: string
  agent_id?: string
}

export interface UsageResponse {
  days: number
  from: string
  to: string
  totals: UsageTotals
  today: UsageTotals
  daily: Array<UsageTotals & { day: string }>
  by_provider: UsageBreakdown[]
  by_model: UsageBreakdown[]
  by_channel: UsageBreakdown[]
  by_agent: UsageBreakdown[]
  governance: {
    session_token_budget: number
    daily_token_budget: number
    on_budget_exceeded: string
  }
}

export async function getUsage(days: number): Promise<UsageResponse> {
  const response = await launcherFetch(`/api/usage?days=${days}`)
  if (!response.ok) {
    throw new Error((await response.text()).trim() || "Failed to load usage")
  }
  return (await response.json()) as UsageResponse
}
