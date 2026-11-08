import { describe, expect, test } from "bun:test"

import {
  isLauncherAuthPathname,
  isLauncherLoginPathname,
  isLauncherSetupPathname,
  normalizePathname,
} from "./launcher-login-path"

describe("launcher-login-path", () => {
  test("normalizePathname strips trailing slashes", () => {
    expect(normalizePathname("/launcher-login/")).toBe("/launcher-login")
    expect(normalizePathname("/")).toBe("/")
    expect(normalizePathname("///")).toBe("/")
  })

  test("isLauncherLoginPathname checks login path", () => {
    expect(isLauncherLoginPathname("/launcher-login")).toBe(true)
    expect(isLauncherLoginPathname("/launcher-login/")).toBe(true)
    expect(isLauncherLoginPathname("/other")).toBe(false)
  })

  test("isLauncherSetupPathname checks setup path", () => {
    expect(isLauncherSetupPathname("/launcher-setup")).toBe(true)
    expect(isLauncherSetupPathname("/launcher-setup/")).toBe(true)
    expect(isLauncherSetupPathname("/login")).toBe(false)
  })

  test("isLauncherAuthPathname checks auth flows", () => {
    expect(isLauncherAuthPathname("/launcher-login")).toBe(true)
    expect(isLauncherAuthPathname("/launcher-setup/")).toBe(true)
    expect(isLauncherAuthPathname("/dashboard")).toBe(false)
  })
})
