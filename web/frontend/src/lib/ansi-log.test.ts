import { describe, expect, test } from "bun:test"

import { parseAnsiSegments } from "./ansi-log"

describe("parseAnsiSegments", () => {
  test("parses plain text without ansi escapes", () => {
    const result = parseAnsiSegments("hello world")
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe("hello world")
    expect(result[0].style).toEqual({})
  })

  test("parses ansi colored text", () => {
    const colored = "\u001b[31mError message\u001b[0m"
    const result = parseAnsiSegments(colored)
    expect(result.length).toBeGreaterThan(0)
    expect(result.some((s) => s.text.includes("Error message"))).toBeTrue()
  })
})
