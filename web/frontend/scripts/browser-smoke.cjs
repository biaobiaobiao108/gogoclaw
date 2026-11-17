// Run against a local Vite server with a globally installed Playwright.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE)

async function main() {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.GOGOCLAW_BROWSER_EXECUTABLE
      ? { executablePath: process.env.GOGOCLAW_BROWSER_EXECUTABLE }
      : {}),
  })
  try {
    const page = await browser.newPage({
      viewport: { width: 1280, height: 800 },
    })
    const errors = []
    const flows = []
    let authenticated = false
    let authServiceFailure = false
    let usageFailuresRemaining = 0
    let expectUsageFailure = false
    let chatMessage = ""
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("console", (message) => {
      const text = message.text()
      const expectedAuthFailure = authServiceFailure && text.includes("503")
      const expectedUsageFailure =
        expectUsageFailure &&
        text.includes("500") &&
        message.location().url.includes("/api/usage")
      if (
        message.type() === "error" &&
        !text.includes("401") &&
        !expectedAuthFailure &&
        !expectedUsageFailure
      ) {
        errors.push(message.text())
      }
    })
    await page.routeWebSocket("**/pico/ws*", (socket) => {
      socket.onMessage((raw) => {
        const message = JSON.parse(raw)
        if (message.type === "message.send") {
          chatMessage = message.payload.content
          socket.send(
            JSON.stringify({
              type: "message.create",
              payload: { message_id: "reply-1", content: "Mock reply" },
            }),
          )
        }
      })
    })
    await page.route("**/api/**", async (route) => {
      const request = route.request()
      const path = new URL(request.url()).pathname
      if (!path.startsWith("/api/")) return route.continue()
      let status = 200
      let body = {}
      if (path === "/api/auth/status") {
        if (authServiceFailure) {
          status = 503
          body = { error: "unavailable" }
        } else {
          body = { initialized: true, authenticated }
        }
      } else if (path === "/api/auth/login") {
        authenticated = JSON.parse(request.postData()).password === "correct"
        status = authenticated ? 200 : 401
        body = authenticated ? { ok: true } : { error: "invalid password" }
      } else if (path === "/api/oauth/providers") {
        body = { providers: [] }
      } else if (path.startsWith("/api/oauth/flows/")) {
        const flowID = path.split("/").pop()
        flows.push(flowID)
        body = {
          flow_id: flowID,
          provider: "openai",
          method: "browser",
          status: "success",
        }
      } else if (path === "/api/gateway/status") {
        body = { gateway_status: "running" }
      } else if (path === "/api/models") {
        body = {
          total: 1,
          default_model: "test",
          default_provider: "openai",
          provider_options: [
            {
              id: "openai",
              display_name: "OpenAI",
              default_api_base: "https://api.openai.com/v1",
              empty_api_key_allowed: false,
              create_allowed: true,
              default_model_allowed: true,
              supports_fetch: true,
              default_auth_method: "api_key",
              common_models: ["test"],
            },
          ],
          models: [
            {
              index: 0,
              model_name: "test",
              model: "test",
              enabled: true,
              available: true,
              status: "available",
              is_default: true,
            },
          ],
        }
      } else if (path === "/api/usage") {
        if (usageFailuresRemaining > 0) {
          usageFailuresRemaining--
          status = 500
          body = { error: "usage unavailable" }
        } else {
          body = {
            days: 30,
            from: "2026-01-01",
            to: "2026-01-30",
            totals: zeroUsage(),
            today: zeroUsage(),
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
      } else if (path === "/api/sessions") {
        body = { items: [], has_more: false }
      } else if (path.startsWith("/api/sessions/")) {
        body = { messages: [], has_more: false }
      }
      await route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      })
    })

    const base = process.env.GOGOCLAW_BROWSER_URL || "http://127.0.0.1:5173"
    await page.goto(`${base}/launcher-login`)
    await page.locator("#launcher-password").fill("wrong")
    await page.locator('button[type="submit"]').click()
    await page.locator('[role="alert"]').waitFor()
    await page.locator("#launcher-password").fill("correct")
    await page.locator('button[type="submit"]').click()
    await page.waitForURL(`${base}/`)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.keyboard.press("Escape")
    const skip = page.getByRole("button", { name: /跳过引导|Skip/i })
    await skip.waitFor({ state: "visible" })
    await skip.click()
    const composer = page.locator('textarea:not([aria-hidden="true"])')
    await composer.waitFor({ state: "visible" })
    await composer.fill("Hello from browser")
    await composer.press("Enter")
    await page.getByText("Mock reply", { exact: true }).waitFor()
    if (chatMessage !== "Hello from browser")
      throw new Error("Chat message was not sent")

    const mobileOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    )
    if (mobileOverflow) throw new Error("Chat overflows the mobile viewport")

    await page.keyboard.press("Escape")
    await page.setViewportSize({ width: 320, height: 800 })
    await page.getByRole("button", { name: /更多操作|More actions/i }).click()
    await page.getByRole("menuitem", { name: "简体中文" }).click()
    await page.waitForFunction(() => document.documentElement.lang === "zh-CN")

    usageFailuresRemaining = 2
    expectUsageFailure = true
    await page.goto(`${base}/usage`)
    const usageTitle = page.getByRole("heading", { name: "用量与成本" })
    await usageTitle.waitFor()
    await page.getByText("用量数据加载失败", { exact: true }).waitFor()
    await page.getByRole("button", { name: "重试", exact: true }).click()
    await page.getByText("所选区间没有用量", { exact: true }).waitFor()
    expectUsageFailure = false
    const usageBody = await page.locator("body").innerText()
    if (/\busage\.[A-Za-z]/.test(usageBody))
      throw new Error("Usage page renders raw translation keys")
    const usageLayout = await usageTitle.evaluate((element) => ({
      clientWidth: element.clientWidth,
      scrollWidth: element.scrollWidth,
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: innerWidth,
    }))
    if (usageLayout.scrollWidth > usageLayout.clientWidth + 1)
      throw new Error("Usage title is truncated at 320px")
    if (usageLayout.documentWidth > usageLayout.viewportWidth)
      throw new Error("Usage page overflows the 320px viewport")

    await page.goto(`${base}/models`)
    const addModelButton = page.getByRole("button", {
      name: "添加模型",
      exact: true,
    })
    await addModelButton.waitFor()
    await addModelButton.click()
    const addModelSheet = page.getByRole("dialog")
    await addModelSheet.waitFor()
    await page.getByRole("button", { name: "关闭", exact: true }).waitFor()
    if (
      (await page
        .getByText("当前修改尚未保存，保存后才会写入模型配置。", {
          exact: true,
        })
        .count()) !== 0
    )
      throw new Error("Add model sheet starts in a dirty state")
    const modelNameInput = addModelSheet.locator("input").first()
    const modelNameLabel = await modelNameInput.evaluate((element) => {
      const labelledBy = element.getAttribute("aria-labelledby")
      return labelledBy
        ? document.getElementById(labelledBy)?.textContent?.trim()
        : ""
    })
    if (modelNameLabel !== "模型别名")
      throw new Error("Add model name input has no localized accessible label")
    await modelNameInput.fill("browser-model")
    await page
      .getByText("当前修改尚未保存，保存后才会写入模型配置。", {
        exact: true,
      })
      .waitFor()
    await page.keyboard.press("Escape")
    await addModelSheet.waitFor({ state: "detached" })
    const focusAfterClose = await page.evaluate(() =>
      document.activeElement?.getAttribute("aria-label"),
    )
    if (focusAfterClose !== "添加模型")
      throw new Error("Add model sheet did not restore focus to its trigger")

    await page.getByRole("button", { name: "更多操作" }).click()
    await page.getByRole("menuitem", { name: "English" }).click()
    await page.waitForFunction(() => document.documentElement.lang === "en")

    await page.goto(`${base}/credentials`)
    await page.getByText("OpenAI", { exact: true }).first().waitFor()
    const validFlow = page.waitForResponse((response) =>
      response.url().endsWith("/api/oauth/flows/valid"),
    )
    await page.evaluate(() =>
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: "https://other.example",
          data: { type: "gogoclaw-oauth-result", flowId: "foreign" },
        }),
      ),
    )
    await page.waitForTimeout(200)
    if (flows.includes("foreign"))
      throw new Error("Cross-origin OAuth message was accepted")
    await page.evaluate(() =>
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: window.location.origin,
          data: { type: "gogoclaw-oauth-result", flowId: "valid" },
        }),
      ),
    )
    await validFlow

    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: /更多操作|More actions/i }).click()
    await page.getByRole("menuitem", { name: "简体中文" }).click()
    await page.waitForFunction(() => document.documentElement.lang === "zh-CN")
    authServiceFailure = true
    await page.goto(`${base}/`)
    await page.getByText("认证服务错误", { exact: false }).waitFor()
    const authError = page.getByText("认证服务错误", { exact: false })
    await page.getByRole("button", { name: "关闭提示" }).click()
    await authError.waitFor({ state: "detached" })

    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log(
      "Browser smoke passed: login, mobile chat, usage states, model form focus, language switching, auth error dismissal and OAuth message origin",
    )
  } finally {
    await browser.close()
  }
}

function zeroUsage() {
  return {
    calls: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cost_nanos: 0,
    cost_usd: 0,
  }
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
