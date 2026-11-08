import { describe, expect, test } from "bun:test"

import { smoothRevealCount, smoothRevealDuration } from "@/lib/smooth-stream"

describe("smooth stream reveal", () => {
  test("scales duration with the incoming text size", () => {
    expect(smoothRevealDuration(0, 1)).toBe(80)
    expect(smoothRevealDuration(0, 40)).toBe(200)
    expect(smoothRevealDuration(0, 200)).toBe(420)
  })

  test("reveals an easing prefix without overshooting", () => {
    expect(smoothRevealCount(10, 30, 0)).toBe(10)
    expect(smoothRevealCount(10, 30, 0.5)).toBe(28)
    expect(smoothRevealCount(10, 30, 1)).toBe(30)
    expect(smoothRevealCount(10, 30, 2)).toBe(30)
  })
})
