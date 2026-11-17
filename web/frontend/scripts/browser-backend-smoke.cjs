// Exercise the embedded UI and real launcher with an isolated data directory.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE)
const { spawn } = require("node:child_process")
const fs = require("node:fs/promises")
const net = require("node:net")
const os = require("node:os")
const path = require("node:path")

async function unusedPort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const port = server.address().port
  await new Promise((resolve, reject) =>
    server.close((e) => (e ? reject(e) : resolve())),
  )
  return port
}

async function main() {
  const launcher = process.env.GOGOCLAW_TEST_LAUNCHER
  const cli = process.env.GOGOCLAW_TEST_CLI
  if (!launcher || !cli)
    throw new Error("Set GOGOCLAW_TEST_LAUNCHER and GOGOCLAW_TEST_CLI")
  const home = await fs.mkdtemp(path.join(os.tmpdir(), "gogoclaw-browser-"))
  const port = await unusedPort()
  const base = `http://127.0.0.1:${port}`
  let output = ""
  let child
  let browser
  try {
    child = spawn(
      launcher,
      ["-host", "127.0.0.1", "-port", String(port), "-no-browser"],
      {
        env: {
          ...process.env,
          GOGOCLAW_HOME: home,
          GOGOCLAW_CONFIG: path.join(home, "config.json"),
          GOGOCLAW_BINARY: cli,
        },
        stdio: ["ignore", "pipe", "pipe"],
      },
    )
    child.stdout.on("data", (data) => {
      output += data
    })
    child.stderr.on("data", (data) => {
      output += data
    })
    let token
    for (let i = 0; i < 300; i++) {
      token = output.match(/#setup_token=([^\s]+)/)?.[1]
      if (token) {
        try {
          if ((await fetch(`${base}/api/auth/status`)).ok) break
        } catch {}
      }
      if (child.exitCode !== null) throw new Error(`Launcher exited: ${output}`)
      await new Promise((resolve) => setTimeout(resolve, 100))
    }
    if (!token) throw new Error(`Launcher setup token missing: ${output}`)
    browser = await chromium.launch({
      headless: true,
      ...(process.env.GOGOCLAW_BROWSER_EXECUTABLE
        ? { executablePath: process.env.GOGOCLAW_BROWSER_EXECUTABLE }
        : {}),
    })
    const page = await browser.newPage({
      viewport: { width: 390, height: 844 },
    })
    const errors = []
    const configWrites = []
    page.on("request", (request) => {
      if (request.method() === "PATCH" && request.url().endsWith("/api/config"))
        configWrites.push(request.postData())
    })
    page.on("pageerror", (error) => errors.push(error.message))
    await page.goto(
      `${base}/launcher-setup#setup_token=${encodeURIComponent(token)}`,
    )
    await page.locator("#setup-password").fill("browser-test-password")
    await page.locator("#setup-confirm").fill("browser-test-password")
    await page.locator('button[type="submit"]').click()
    await page.waitForURL(`${base}/launcher-login`)
    await page.locator("#launcher-password").fill("incorrect-password")
    await page.locator('button[type="submit"]').click()
    await page.locator('[role="alert"]').waitFor()
    await page.locator("#launcher-password").fill("browser-test-password")
    await page.locator('button[type="submit"]').click()
    await page.waitForURL(`${base}/chat`)
    const skip = page.getByRole("button", { name: /跳过引导|Skip/i })
    await skip.waitFor()
    await skip.click()
    const status = await page.evaluate(async () =>
      (await fetch("/api/auth/status")).json(),
    )
    if (!status.initialized || !status.authenticated)
      throw new Error("Real backend did not authenticate the browser")
    const create = await page.evaluate(async () => {
      const response = await fetch("/api/models", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          model_name: "browser-test-model",
          provider: "openai",
          model: "test-model",
          enabled: false,
        }),
      })
      return { ok: response.ok, error: await response.text() }
    })
    if (!create.ok)
      throw new Error(`Could not save test model: ${create.error}`)
    await page.goto(`${base}/models`)
    await page
      .getByText("browser-test-model", { exact: true })
      .first()
      .waitFor()
    await page.reload()
    await page
      .getByText("browser-test-model", { exact: true })
      .first()
      .waitFor()
    const preset = await page.evaluate(
      async () =>
        (
          await fetch("/api/config", {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              tools: {
                approval: {
                  enabled: false,
                  tools: ["exec"],
                  timeout_seconds: 123,
                },
              },
            }),
          })
        ).ok,
    )
    if (!preset) throw new Error("Could not set up the approval policy")
    await page.goto(`${base}/config`)
    const approval = page.getByRole("switch", {
      name: /Tool approval|工具调用审批/,
    })
    await approval.waitFor()
    if (await approval.isChecked())
      throw new Error("Approval should initially be disabled")
    await approval.click()
    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/config") &&
        response.request().method() === "PATCH",
    )
    await page
      .getByRole("button", { name: /^(Save|保存)$/ })
      .first()
      .click()
    const saved = await saveResponse
    if (!saved.ok())
      throw new Error(
        `Approval save failed: ${saved.status()} ${await saved.text()}`,
      )
    await page.waitForFunction(async () => {
      const response = await fetch("/api/config")
      if (!response.ok) return false
      const config = await response.json()
      return config.tools?.approval?.enabled === true
    })
    await page
      .getByRole("button", { name: /^(Save|保存)$/ })
      .first()
      .waitFor({ state: "visible" })
    await page.waitForFunction(
      () =>
        ![...document.querySelectorAll("button")].some((button) =>
          /^(Saving|保存中)/.test(button.textContent.trim()),
        ),
    )
    const policy = await page.evaluate(
      async () => (await (await fetch("/api/config")).json()).tools.approval,
    )
    if (policy.tools?.[0] !== "exec" || policy.timeout_seconds !== 123)
      throw new Error("Saving approval changed the custom policy")
    await page.reload()
    await page
      .waitForFunction(
        () => {
          const switches = [...document.querySelectorAll('[role="switch"]')]
          return switches.some(
            (element) =>
              /Tool approval|工具调用审批/.test(
                element.getAttribute("aria-label") || "",
              ) && element.getAttribute("aria-checked") === "true",
          )
        },
        null,
        { timeout: 5000 },
      )
      .catch(async () => {
        const detail = await page.evaluate(async () => ({
          policy: (await (await fetch("/api/config")).json()).tools.approval,
          switches: [...document.querySelectorAll('[role="switch"]')].map(
            (element) => [
              element.getAttribute("aria-label"),
              element.getAttribute("aria-checked"),
            ],
          ),
        }))
        throw new Error(
          `Approval reload failed at ${page.url()}: ${JSON.stringify(detail)} writes=${JSON.stringify(configWrites.map((value) => JSON.parse(value).tools?.approval))}`,
        )
      })
    if (
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      )
    )
      throw new Error("Mobile page overflows")
    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log(
      "Real-backend browser smoke passed: setup, login, model persistence and approval settings",
    )
  } finally {
    if (browser) await browser.close()
    if (child && child.exitCode === null) {
      child.kill()
      await new Promise((resolve) => child.once("exit", resolve))
    }
    await fs.rm(home, { recursive: true, force: true })
  }
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
