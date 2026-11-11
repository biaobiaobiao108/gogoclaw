// Run against a local Vite server with a globally installed Playwright.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE)

async function main() {
  const browser = await chromium.launch({ headless: true })
  try {
    const page = await browser.newPage({
      viewport: { width: 1280, height: 800 },
    })
    const errors = []
    const flows = []
    let authenticated = false
    let chatMessage = ""
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("console", (message) => {
      if (message.type() === "error" && !message.text().includes("401")) {
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
        body = { initialized: true, authenticated }
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
          default_model: "test",
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
    const skip = page.getByRole("button", { name: /跳过引导|Skip/i })
    if (await skip.isVisible()) await skip.click()
    const composer = page.locator('textarea:not([aria-hidden="true"])')
    await composer.waitFor({ state: "visible" })
    await composer.fill("Hello from browser")
    await composer.press("Enter")
    await page.getByText("Mock reply", { exact: true }).waitFor()
    if (chatMessage !== "Hello from browser")
      throw new Error("Chat message was not sent")

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
    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log("Browser smoke passed: login, chat and OAuth message origin")
  } finally {
    await browser.close()
  }
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
