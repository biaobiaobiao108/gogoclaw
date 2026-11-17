import { describe, expect, test } from "bun:test"

import { modelStatusLabel } from "./model-status"

const labels: Record<string, string> = {
  "models.status.available": "Available",
  "models.status.unconfigured": "Not configured",
  "models.status.unreachable": "Service unreachable",
  "models.status.unknown": "Unknown status",
}

describe("modelStatusLabel", () => {
  test.each(["available", "unconfigured", "unreachable"])(
    "localizes %s",
    (status) => {
      expect(modelStatusLabel(status, (key) => labels[key] ?? key)).toBe(
        labels[`models.status.${status}`],
      )
    },
  )

  test("does not expose a raw translation key for an unknown status", () => {
    const label = modelStatusLabel("future-state", (key) => labels[key] ?? key)
    expect(label).toBe("Unknown status")
    expect(label).not.toContain("models.status.")
  })
})
