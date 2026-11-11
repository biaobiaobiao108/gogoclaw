import { beforeEach, describe, expect, test } from "bun:test"

import {
  clearCachedRowSizes,
  clearSessionViewport,
  getCachedRowSize,
  getSessionViewport,
  saveSessionViewport,
  setCachedRowSize,
} from "./viewport"

describe("chat viewport and row size cache", () => {
  beforeEach(() => {
    clearCachedRowSizes()
    clearSessionViewport("test-session")
  })

  test("stores and restores session viewport snapshot", () => {
    expect(getSessionViewport("test-session")).toBeUndefined()

    saveSessionViewport("test-session", {
      scrollTop: 350,
      isAtBottom: false,
      hasScrolled: true,
      autoScrollEnabled: false,
      expandedProcesses: { "p-1": true },
    })

    const snapshot = getSessionViewport("test-session")
    expect(snapshot).toBeDefined()
    expect(snapshot?.scrollTop).toBe(350)
    expect(snapshot?.isAtBottom).toBe(false)
    expect(snapshot?.hasScrolled).toBe(true)
    expect(snapshot?.autoScrollEnabled).toBe(false)
    expect(snapshot?.expandedProcesses).toEqual({ "p-1": true })
  })

  test("partially updates existing session viewport snapshot", () => {
    saveSessionViewport("test-session", {
      scrollTop: 100,
      isAtBottom: true,
    })

    saveSessionViewport("test-session", {
      scrollTop: 500,
      isAtBottom: false,
    })

    const snapshot = getSessionViewport("test-session")
    expect(snapshot?.scrollTop).toBe(500)
    expect(snapshot?.isAtBottom).toBe(false)
    expect(snapshot?.autoScrollEnabled).toBe(true)
  })

  test("caches and returns row measurements", () => {
    expect(getCachedRowSize("row-1", 112)).toBe(112)

    setCachedRowSize("row-1", 240)
    expect(getCachedRowSize("row-1", 112)).toBe(240)

    clearCachedRowSizes()
    expect(getCachedRowSize("row-1", 112)).toBe(112)
  })
})
