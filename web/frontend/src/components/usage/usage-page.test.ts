import { describe, expect, test } from "bun:test"

import type { UsageResponse } from "@/api/usage"

import { formatCost, hasUsageActivity } from "./usage-format"

describe("formatCost", () => {
  test("does not display small non-zero amounts as free", () => {
    expect(formatCost(0)).toBe("$0.00")
    expect(formatCost(0.000000045)).toBe("<$0.0001")
    expect(formatCost(0.00045)).toBe("$0.00045")
  })

  test("uses cents for ordinary amounts", () => {
    expect(formatCost(1.234)).toBe("$1.23")
  })
})

describe("hasUsageActivity", () => {
  test("treats zero-valued range data as empty", () => {
    expect(hasUsageActivity(emptyUsage())).toBe(false)
  })

  test("detects usage in totals or breakdowns", () => {
    const totals = emptyUsage()
    totals.totals.calls = 1
    expect(hasUsageActivity(totals)).toBe(true)

    const breakdown = emptyUsage()
    breakdown.by_model = [{ ...zeroTotals(), model: "test" }]
    expect(hasUsageActivity(breakdown)).toBe(true)
  })
})

function zeroTotals() {
  return {
    calls: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cost_nanos: 0,
    cost_usd: 0,
  }
}

function emptyUsage(): UsageResponse {
  return {
    days: 30,
    from: "2026-01-01",
    to: "2026-01-30",
    totals: zeroTotals(),
    today: zeroTotals(),
    daily: [],
    by_provider: [],
    by_model: [],
    by_channel: [],
    by_agent: [],
    governance: {
      session_token_budget: 0,
      daily_token_budget: 0,
      on_budget_exceeded: "warn",
    },
  }
}
