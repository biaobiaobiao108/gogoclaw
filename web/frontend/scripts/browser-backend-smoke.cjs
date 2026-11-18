// Exercise the embedded UI and real launcher with an isolated data directory.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE)
const { spawn } = require("node:child_process")
const fs = require("node:fs/promises")
const net = require("node:net")
const http = require("node:http")
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

const chatSentinel = "private-browser-input-91372"
const replySentinel = "private-browser-answer-63725"

async function startModelFixture() {
  const server = http.createServer(async (request, response) => {
    if (request.method === "GET" && request.url === "/v1/models") {
      response.writeHead(200, { "Content-Type": "application/json" })
      response.end(JSON.stringify({ data: [{ id: "fixture-chat" }] }))
      return
    }
    if (request.method !== "POST" || request.url !== "/v1/chat/completions") {
      response.writeHead(404).end()
      return
    }
    const chunks = []
    for await (const chunk of request) chunks.push(chunk)
    const body = Buffer.concat(chunks).toString("utf8")
    if (!body.includes(chatSentinel)) {
      response.writeHead(400).end()
      return
    }
    response.writeHead(200, { "Content-Type": "application/json" })
    response.end(
      JSON.stringify({
        id: "fixture-completion",
        object: "chat.completion",
        model: "fixture-chat",
        choices: [
          {
            index: 0,
            message: { role: "assistant", content: replySentinel },
            finish_reason: "stop",
          },
        ],
        usage: { prompt_tokens: 20, completion_tokens: 10, total_tokens: 30 },
      }),
    )
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  return server
}

async function checkLocalChat(page, base, model) {
  const gatewayPort = await unusedPort()
  const modelBase = `http://127.0.0.1:${model.address().port}/v1`
  const prepare = await page.evaluate(
    async ({ gatewayPort, modelBase }) => {
      async function write(path, method, body) {
        const response = await fetch(path, {
          method,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        })
        return response.ok ? null : `${path}: ${await response.text()}`
      }
      return (
        (await write("/api/config", "PATCH", {
          gateway: { host: "127.0.0.1", port: gatewayPort, log_level: "debug" },
        })) ??
        (await write("/api/models", "POST", {
          model_name: "browser-loopback-model",
          provider: "openai",
          model: "fixture-chat",
          api_base: modelBase,
          api_key: "fixture-key",
          streaming: { enabled: false },
        })) ??
        (await write("/api/models/default", "POST", {
          model_name: "browser-loopback-model",
        }))
      )
    },
    { gatewayPort, modelBase },
  )
  if (prepare) throw new Error(prepare)
  await page.goto(`${base}/chat`)
  await page
    .locator("main")
    .getByRole("button", { name: /Start Gateway|启动服务/ })
    .click({ timeout: 10000 })
  await page.waitForFunction(
    async () => {
      const response = await fetch("/api/gateway/readiness")
      return response.ok && (await response.json()).status === "ready"
    },
    null,
    { timeout: 15000, polling: 500 },
  )
  const input = page.locator('textarea:not([aria-hidden="true"])')
  await input.fill(chatSentinel, { timeout: 10000 })
  await input.press("Enter")
  await page
    .getByText(replySentinel, { exact: true })
    .waitFor({ timeout: 15000 })

  const chatURL = page.url()
  await page.reload()
  await page
    .getByText(replySentinel, { exact: true })
    .waitFor({ timeout: 10000 })
    .catch(async (error) => {
      const data = await page.evaluate(async () => {
        const id = localStorage.getItem("gogoclaw:last-session-id")
        const list = await fetch("/api/sessions?limit=30").then((res) =>
          res.text(),
        )
        const detail = id
          ? await fetch(`/api/sessions/${encodeURIComponent(id)}`).then((res) =>
              res.text(),
            )
          : ""
        return {
          id,
          list,
          detail,
          body: document.body.innerText.slice(0, 1000),
        }
      })
      throw new Error(
        `Chat reload at ${chatURL} failed: ${JSON.stringify(data)}`,
        { cause: error },
      )
    })
  const logOutput = await page.evaluate(async () =>
    (await fetch("/api/gateway/logs")).json(),
  )
  const log = (logOutput.logs ?? []).join("\n")
  if (!log.includes("LLM request") || !log.includes("turn_id"))
    throw new Error("Launcher log output is missing turn diagnostics")
  if (log.includes(chatSentinel) || log.includes(replySentinel))
    throw new Error("Launcher log output contains conversation content")
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
  let page
  let model
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
          if ((await fetch(`${base}/health`)).ok) break
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
    page = await browser.newPage({
      viewport: { width: 390, height: 844 },
    })
    await page.addInitScript(() => localStorage.setItem("theme", "dark"))
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
    if (
      !(await page.evaluate(() =>
        document.documentElement.classList.contains("dark"),
      ))
    )
      throw new Error("Theme initialization did not run under CSP")
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
    const modelSetup = page.getByRole("link", {
      name: /Go to Models|去模型页配置/i,
    })
    await modelSetup.waitFor()
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 844 })
      if (!(await modelSetup.isVisible()))
        throw new Error(`First-run model action missing at ${width}px`)
    }
    const readiness = await page.evaluate(async () => {
      const response = await fetch("/api/gateway/readiness")
      return { code: response.status, body: await response.json() }
    })
    if (readiness.code !== 503 || readiness.body.status !== "unavailable")
      throw new Error(
        "Unconfigured gateway should report unavailable independently of Launcher health",
      )
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
    await page.goto(`${base}/usage`)
    await page
      .getByText(/No usage in this range|所选区间没有用量/)
      .waitFor()
      .catch(async () => {
        const detail = await page.evaluate(async () => ({
          body: document.body.innerText,
          usage: await fetch("/api/usage?days=30").then(async (response) => ({
            status: response.status,
            body: await response.text(),
          })),
        }))
        throw new Error(
          `Usage empty state missing at ${page.url()}: ${JSON.stringify(detail)}`,
        )
      })
    const usageText = await page.locator("body").innerText()
    if (/\busage\.[A-Za-z]/.test(usageText))
      throw new Error("Usage page renders raw translation keys")
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
    model = await startModelFixture()
    await checkLocalChat(page, base, model)
    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log(
      "Real-backend browser smoke passed: setup, login, model persistence, approval settings, gateway readiness, chat reload and log privacy",
    )
  } finally {
    if (model && page) {
      await page
        .evaluate(() => fetch("/api/gateway/stop", { method: "POST" }))
        .catch(() => {})
      model.closeAllConnections()
      await new Promise((resolve) => model.close(resolve))
    }
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
