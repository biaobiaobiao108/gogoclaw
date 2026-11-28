import { expect, test } from "bun:test"

import {
  postLauncherDashboardLogin,
  postLauncherDashboardSetup,
} from "./launcher-auth"

test("login preserves password whitespace", async () => {
  const originalFetch = globalThis.fetch
  let requestBody = ""
  globalThis.fetch = (async (_input, init) => {
    requestBody = String(init?.body)
    return new Response(null, { status: 200 })
  }) as typeof fetch

  try {
    const result = await postLauncherDashboardLogin("  secret  ")
    expect(result).toEqual({ ok: true })
    expect(JSON.parse(requestBody)).toEqual({ password: "  secret  " })
  } finally {
    globalThis.fetch = originalFetch
  }
})

test("dashboard setup preserves password and current-password whitespace", async () => {
  const originalFetch = globalThis.fetch
  const originalLocation = Object.getOwnPropertyDescriptor(
    globalThis,
    "location",
  )
  let requestBody = ""
  let setupToken = ""
  globalThis.fetch = (async (_input, init) => {
    requestBody = String(init?.body)
    setupToken = new Headers(init?.headers).get("X-Launcher-Setup-Token") ?? ""
    return new Response(null, { status: 200 })
  }) as typeof fetch
  Object.defineProperty(globalThis, "location", {
    configurable: true,
    value: { hash: "#setup_token=test-token" },
  })

  try {
    const result = await postLauncherDashboardSetup(
      "  new-password  ",
      "  new-password  ",
      "  old-password  ",
    )
    expect(result).toEqual({ ok: true })
    expect(setupToken).toBe("test-token")
    expect(JSON.parse(requestBody)).toEqual({
      password: "  new-password  ",
      confirm: "  new-password  ",
      current_password: "  old-password  ",
    })
  } finally {
    globalThis.fetch = originalFetch
    if (originalLocation) {
      Object.defineProperty(globalThis, "location", originalLocation)
    } else {
      Reflect.deleteProperty(globalThis, "location")
    }
  }
})
