import { describe, expect, test } from "bun:test"

import { formatCost } from "./usage-page"

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
