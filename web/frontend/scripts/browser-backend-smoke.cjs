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
const interimSentinel = "browser-interim-accepted-88923"

const stopPrompt = "fixture-stop-test"
const stoppedText = "fixture-partial-kept"
const resumedText = "fixture-finished-without-tools"
const reconnectPrompt = "fixture-reconnect-test"
const reconnectStart = "fixture-stream-before-disconnect"
const reconnectFinal = "fixture-stream-after-reconnect"
const steeringPrompt = "fixture-steering-test"
const steeringInput = "take a different direction"
const steeringStart = "fixture-opening-before-steering"
const steeringFinal = "fixture-answer-with-steering"
const approvalPrompt = "fixture-approval-test"
const approvalText = "fixture-before-approval"
const approvalFinal = "fixture-after-approval"

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
    if (
      ![
        chatSentinel,
        stopPrompt,
        reconnectPrompt,
        approvalPrompt,
        steeringPrompt,
      ].some((value) => body.includes(value))
    ) {
      response.writeHead(400).end()
      return
    }
    const input = JSON.parse(body)
    const hasToolResult = input.messages.some(
      (message) => message.role === "tool",
    )
    const latest =
      input.messages.filter((message) => message.role === "user").at(-1)
        ?.content ?? ""
    const isResume = String(latest).includes("Continue the interrupted answer")
    const isStop = !isResume && body.includes(stopPrompt)
    const isReconnect = body.includes(reconnectPrompt)
    const isApproval = body.includes(approvalPrompt)
    const isSteering = body.includes(steeringPrompt)
    const receivedSteering = body.includes(steeringInput)
    const needsTool = !isResume && !isStop && !isReconnect && !hasToolResult
    const text = isResume
      ? resumedText
      : isStop
        ? stoppedText
        : isReconnect
          ? reconnectStart
          : isApproval
            ? hasToolResult
              ? approvalFinal
              : approvalText
            : isSteering
              ? receivedSteering
                ? steeringFinal
                : hasToolResult
                  ? "superseded content"
                  : steeringStart
              : hasToolResult
                ? replySentinel
                : interimSentinel
    if (isSteering && receivedSteering)
      server.steeringCount = input.messages.filter(
        (message) =>
          message.role === "user" &&
          String(message.content).includes(steeringInput),
      ).length
    if (isResume) {
      server.resumeTools = input.tools?.length ?? 0
      server.resumeHasPartial = body.includes(stoppedText)
    }
    const toolCalls = [
      {
        index: 0,
        id: "call_browser_list",
        type: "function",
        function: {
          name: "list_dir",
          arguments: JSON.stringify({ path: "." }),
        },
      },
    ]
    if (hasToolResult)
      await new Promise((resolve) =>
        setTimeout(resolve, isSteering && !receivedSteering ? 4000 : 1800),
      )
    if (input.stream) {
      response.writeHead(200, { "Content-Type": "text/event-stream" })
      const event = (delta, reason = null) => {
        response.write(
          "data: " +
            JSON.stringify({
              id: "fixture-completion",
              object: "chat.completion.chunk",
              model: "fixture-chat",
              choices: [{ index: 0, delta, finish_reason: reason }],
            }) +
            "\n\n",
        )
      }
      event({ role: "assistant", content: text })
      if (isStop) {
        await new Promise((resolve) => {
          const timeout = setTimeout(resolve, 13000)
          response.once("close", () => {
            clearTimeout(timeout)
            resolve()
          })
        })
        if (response.destroyed) return
      } else if (isReconnect) {
        for (const part of [" hold", " reconnect", " now"]) {
          await new Promise((resolve) => setTimeout(resolve, 1000))
          event({ content: part })
        }
        await new Promise((resolve) => setTimeout(resolve, 2200))
        event({ content: " " + reconnectFinal })
      } else {
        await new Promise((resolve) => setTimeout(resolve, 650))
      }
      if (needsTool) event({ tool_calls: toolCalls })
      event({}, needsTool ? "tool_calls" : "stop")
      response.end("data: [DONE]\n\n")
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
            message: {
              role: "assistant",
              content: text,
              ...(needsTool ? { tool_calls: toolCalls } : {}),
            },
            finish_reason: needsTool ? "tool_calls" : "stop",
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
  const prepare = await page.evaluate(async (port) => {
    const response = await fetch("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        gateway: { host: "127.0.0.1", port, log_level: "debug" },
      }),
    })
    return response.ok ? null : await response.text()
  }, gatewayPort)
  if (prepare) throw new Error(prepare)
  await page.goto(`${base}/models?setup=true`)
  const setup = page
    .getByText(/Choose a model protocol|选择模型协议/)
    .locator("..")
  await setup.getByRole("button", { name: /Add Model|添加模型/i }).click()
  const modelForm = page.getByRole("dialog")
  await modelForm.getByPlaceholder(/my-gpt4/).fill("browser-loopback-model")
  await modelForm.getByRole("combobox").first().click()
  await modelForm
    .getByRole("option", { name: /OpenAI/ })
    .first()
    .click()
  await modelForm
    .getByRole("textbox", { name: /Model Identifier|模型标识符/i })
    .fill("fixture-chat")
  await modelForm
    .getByPlaceholder(/Enter your API key|请输入 API Key/)
    .fill("fixture-key")
  await modelForm
    .getByRole("textbox", { name: /API Base|基础地址/i })
    .fill(modelBase)
  await modelForm
    .getByRole("button", { name: /Add Model|添加模型/i, exact: true })
    .click()
  const openChat = page.getByRole("link", { name: /Open Chat|返回聊天/ })
  await openChat.waitFor()
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket
    window.WebSocket = class extends NativeWebSocket {
      constructor(...args) {
        super(...args)
        if (String(args[0]).includes("session_id=")) window.qaChatSocket = this
      }
    }
  })
  const frames = []
  const acknowledgments = []
  page.on("websocket", (socket) => {
    socket.on("framereceived", ({ payload }) => {
      try {
        frames.push(JSON.parse(payload))
      } catch {}
    })
    socket.on("framesent", ({ payload }) => {
      try {
        const message = JSON.parse(payload)
        if (message.type === "delivery.ack")
          acknowledgments.push(message.payload.delivery_id)
      } catch {}
    })
  })

  await openChat.click()
  await page.waitForURL(`${base}/chat`)
  await page.reload()
  await page
    .locator("main")
    .getByRole("button", { name: /Start Gateway|启动服务/ })
    .click({ timeout: 10000 })
  await page.waitForFunction(
    async () => {
      const response = await fetch("/api/gateway/readiness")
      const readiness = await response.json()
      return (
        response.ok &&
        readiness.status === "ready" &&
        readiness.config_state === "applied" &&
        !readiness.checks?.config_revision
      )
    },
    null,
    { timeout: 15000, polling: 500 },
  )

  const input = page.locator('textarea:not([aria-hidden="true"])')
  await input.fill(chatSentinel, { timeout: 10000 })
  await input.press("Enter")
  await page
    .getByText(interimSentinel, { exact: true })
    .waitFor({ timeout: 15000 })
  const answer = page.locator("article").filter({ hasText: interimSentinel })
  await answer
    .locator("button[aria-expanded]")
    .first()
    .waitFor({ timeout: 10000 })
  if (
    await answer.getByRole("button", { name: /Copy message|复制消息/ }).count()
  )
    throw new Error(
      "The in-progress answer exposes premature controls: " +
        JSON.stringify(
          frames.map((frame) => ({ type: frame.type, ...frame.payload })),
        ),
    )
  await page
    .getByText(replySentinel, { exact: true })
    .waitFor({ timeout: 15000 })
  await page
    .getByText(/Your first Web reply arrived|已收到第一条 Web 回复/)
    .waitFor()
  await page
    .getByRole("link", { name: /Connect an external channel|连接外部频道/ })
    .waitFor()

  const interim = page.getByText(interimSentinel, { exact: true })
  if ((await interim.count()) !== 1)
    throw new Error("Interim text missing or duplicated after final")
  const deadline = Date.now() + 3500
  let delivered
  while (Date.now() < deadline) {
    delivered = [interimSentinel, replySentinel].map((text) =>
      frames.find(
        (frame) => frame.payload?.content === text && frame.payload.delivery_id,
      ),
    )
    if (
      delivered.every(
        (frame) => frame && acknowledgments.includes(frame.payload.delivery_id),
      )
    )
      break
    await new Promise((resolve) => setTimeout(resolve, 30))
  }
  if (
    !delivered.every(
      (frame) => frame && acknowledgments.includes(frame.payload.delivery_id),
    )
  )
    throw new Error("Missing completion acknowledgment for interim or final")
  if (delivered.some((frame) => frame.payload.stream_draft !== false))
    throw new Error("Completed stream did not carry its completion marker")
  if (
    frames.some(
      (frame) => frame.payload?.stream_draft && frame.payload.delivery_id,
    )
  )
    throw new Error("A draft unexpectedly requested a delivery acknowledgment")
  await answer.getByText(replySentinel, { exact: true }).waitFor()
  if ((await answer.count()) !== 1) throw new Error("Live reply is split")
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
  await interim.waitFor({ timeout: 10000 })
  if ((await interim.count()) !== 1)
    throw new Error("Interim text duplicated after reload")
  async function checkOneAnswer() {
    if ((await answer.count()) !== 1)
      throw new Error("Interim text did not stay in one reply")
    await answer.getByText(replySentinel, { exact: true }).waitFor()
    const process = answer.locator(":scope > div > button[aria-expanded]")
    await process.waitFor({ timeout: 10000 }).catch(async (error) => {
      throw new Error(
        "Reply has no tool process: " + (await answer.innerText()),
        {
          cause: error,
        },
      )
    })
    if ((await process.getAttribute("aria-expanded")) !== "false")
      throw new Error("The tool process should be collapsed by default")
    await process.getByText(/^1 (?:calls?|次调用)$/).waitFor({ timeout: 10000 })
    const parts = await answer.textContent()
    const toolPosition = parts.indexOf(await process.textContent())
    if (
      parts.indexOf(interimSentinel) >= toolPosition ||
      toolPosition >= parts.indexOf(replySentinel)
    )
      throw new Error("Reply segments and process are out of order: " + parts)
    const copy = answer.getByRole("button", { name: /Copy message|复制消息/ })
    const regenerate = answer.getByRole("button", {
      name: /Regenerate|重新生成/,
    })
    await copy.waitFor()
    if ((await copy.count()) !== 1 || (await regenerate.count()) !== 1)
      throw new Error("Reply controls appear more than once")
    await process.click()
    await answer.getByText("list_dir", { exact: true }).first().waitFor()
    await process.click()
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
    await copy.focus()
    await copy.click()
    const contents = await page.evaluate(() => navigator.clipboard.readText())
    if (
      contents.replaceAll("\r\n", "\n") !==
      interimSentinel + "\n\n" + replySentinel
    )
      throw new Error(
        "Copy did not include the complete reply: " + JSON.stringify(contents),
      )
  }
  await checkOneAnswer()
  if (process.env.GOGOCLAW_QA_SCREENSHOT)
    await page.screenshot({ path: process.env.GOGOCLAW_QA_SCREENSHOT })
  console.log(
    "Verified one reply before/after reload, chronological process, unique controls, whole-reply copy, and completion receipts",
  )
  await answer.getByRole("button", { name: /Regenerate|重新生成/ }).focus()
  await answer.getByRole("button", { name: /Regenerate|重新生成/ }).click()
  await page
    .getByText(replySentinel, { exact: true })
    .waitFor({ state: "hidden", timeout: 4000 })
  await page
    .getByText(replySentinel, { exact: true })
    .waitFor({ timeout: 15000 })
  if ((await interim.count()) !== 1)
    throw new Error("Regeneration left the old interim text")
  console.log("Verified regeneration replaced the whole reply")
  const newChat = () =>
    page
      .getByRole("button", { name: /^(New Chat|新建对话)$/ })
      .first()
      .click()
  const send = async (text) => {
    await input.fill(text, { timeout: 10000 })
    await input.press("Enter")
  }
  await newChat()
  await send(reconnectPrompt)
  await page
    .locator("article")
    .getByText(reconnectStart, { exact: true })
    .waitFor({ timeout: 15000 })
  await page.evaluate(() =>
    window.qaChatSocket.close(1000, "fixture disconnect"),
  )
  await page
    .getByText(/Reconnecting to the task|正在重新连接任务/)
    .waitFor({ timeout: 4000 })
  await page.getByText(new RegExp(reconnectFinal)).waitFor({ timeout: 15000 })
  if (
    (await page
      .locator("article")
      .filter({ hasText: reconnectStart })
      .count()) !== 1
  )
    throw new Error("Reconnect split the reply")
  console.log("Verified socket reconnect")

  await newChat()
  await send(steeringPrompt)
  const steeringReply = page
    .locator("article")
    .filter({ hasText: steeringStart })
  await steeringReply
    .locator("button[aria-expanded]")
    .first()
    .waitFor({ timeout: 10000 })
  await page
    .getByPlaceholder(/Add instructions for this task|为当前任务补充指令/)
    .waitFor()
  await send(steeringInput)
  await steeringReply
    .getByText(steeringFinal, { exact: true })
    .waitFor({ timeout: 18000 })
  if (
    (await steeringReply.getByText(steeringInput, { exact: true }).count()) !==
    1
  )
    throw new Error(
      "The steering instruction appears more than once or is missing",
    )
  await page.reload()
  await steeringReply
    .getByText(steeringFinal, { exact: true })
    .waitFor({ timeout: 15000 })
  if (
    (await steeringReply.getByText(steeringInput, { exact: true }).count()) !==
    1
  )
    throw new Error(
      "The steering instruction appears more than once or is missing",
    )
  if (model.steeringCount !== 1)
    throw new Error(
      "The model did not receive the steering instruction exactly once",
    )
  console.log(
    "Verified additional instructions stayed in the live and saved reply",
  )

  await newChat()
  await send(stopPrompt)
  await page
    .locator("article")
    .getByText(stoppedText, { exact: true })
    .waitFor({ timeout: 15000 })
  await page.getByRole("button", { name: /^(Stop task|停止任务)$/i }).click()
  const continuation = page.getByRole("button", {
    name: /^(Continue writing|继续写完回答)$/,
  })
  await continuation.waitFor({ timeout: 10000 })
  await page.reload()
  await page
    .locator("article")
    .getByText(stoppedText, { exact: true })
    .waitFor({ timeout: 15000 })
  if (await page.getByText("/stop", { exact: true }).count())
    throw new Error("Stop leaked a command message")
  await continuation.click()
  await page.getByText(resumedText, { exact: true }).waitFor({ timeout: 15000 })
  if (model.resumeTools !== 0 || !model.resumeHasPartial)
    throw new Error("Continuation must use saved text without providing tools")
  const stoppedReply = page.locator("article").filter({ hasText: stoppedText })
  await stoppedReply.getByText(resumedText, { exact: true }).waitFor()
  const savedDeadline = Date.now() + 10000
  for (;;) {
    const saved = await page.evaluate(async (text) => {
      const id = localStorage.getItem("gogoclaw:last-session-id")
      if (!id) return false
      const response = await fetch(`/api/sessions/${encodeURIComponent(id)}`)
      if (!response.ok) return false
      const { messages } = await response.json()
      return messages.some((message) => message.content === text)
    }, resumedText)
    if (saved) break
    if (Date.now() >= savedDeadline)
      throw new Error("Continuation was not saved")
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  await page.reload()
  await stoppedReply.getByText(resumedText, { exact: true }).waitFor()
  console.log("Verified stop and reload, then text-only continuation")

  await newChat()
  await page.evaluate(async () => {
    const r = await fetch("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        tools: {
          approval: {
            enabled: true,
            yolo: false,
            tools: ["list_dir"],
            timeout_seconds: 90,
          },
        },
      }),
    })
    if (!r.ok)
      throw new Error(
        "Failed to set fixture approval policy: " + (await r.text()),
      )
  })
  await page.goto(`${base}/config`)
  const configuration = page.getByRole("region", {
    name: /Gateway configuration|Gateway 配置生效状态/,
  })
  await configuration
    .getByText(
      /Some saved settings have yet to take effect|已保存的配置尚未全部生效/,
    )
    .waitFor()
  await configuration
    .getByRole("button", { name: /Restart and apply|重启并应用/ })
    .click()
  await page.waitForFunction(
    async () => {
      const response = await fetch("/api/gateway/readiness")
      const readiness = await response.json()
      return (
        response.ok &&
        readiness.status === "ready" &&
        readiness.config_state === "applied"
      )
    },
    null,
    { timeout: 15000, polling: 500 },
  )
  await configuration
    .getByText(
      /Gateway has loaded the saved configuration|Gateway 已加载当前保存的配置/,
    )
    .waitFor()
  const access = await page.evaluate(async () => {
    const response = await fetch("/api/tools")
    const { tools } = await response.json()
    return tools?.find((tool) => tool.name === "list_dir")
  })
  if (access?.access !== "approval" || access.access_source !== "approval")
    throw new Error(
      "The saved tool policy did not show the configured approval",
    )
  await page.goto(`${base}/chat`)
  await send(approvalPrompt)
  const approve = page.getByRole("button", {
    name: /^(Approve once|批准一次)$/i,
  })
  await approve.waitFor({ timeout: 15000 }).catch(async (error) => {
    const status = frames
      .filter(
        (frame) =>
          frame.type === "approval.request" || frame.type === "turn.status",
      )
      .slice(-5)
    throw new Error(
      "Approval prompt did not appear: " + JSON.stringify(status),
      { cause: error },
    )
  })
  await page
    .getByText(/Waiting for approval|等待批准/)
    .first()
    .waitFor()
  await page.reload()
  await page
    .locator("article")
    .getByText(approvalText, { exact: true })
    .waitFor({ timeout: 15000 })
  await approve.waitFor({ timeout: 15000 })
  await approve.click()
  await page
    .getByText(approvalFinal, { exact: true })
    .waitFor({ timeout: 15000 })
  console.log("Verified approval after reload")
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
      locale: "en-US",
      viewport: {
        width: Number(process.env.GOGOCLAW_QA_WIDTH || 390),
        height: 844,
      },
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
    const removedCredentialAPI = await page.request.get(
      `${base}/api/oauth/providers`,
    )
    if (removedCredentialAPI.status() !== 404)
      throw new Error("Removed provider credential API should return 404")
    await page.setViewportSize({ width: 1280, height: 844 })
    if (await page.locator('a[href="/credentials"]').count())
      throw new Error("Removed credentials page is still in the navigation")
    await page.setViewportSize({
      width: Number(process.env.GOGOCLAW_QA_WIDTH || 390),
      height: 844,
    })
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
    await page.setViewportSize({
      width: Number(process.env.GOGOCLAW_QA_WIDTH || 390),
      height: 844,
    })
    await checkLocalChat(page, base, model)
    await checkEvolutionWorkflow(page, base)
    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log(
      "Real-backend browser smoke passed: setup, login, model persistence, approval settings, gateway readiness, chat reload, log privacy and evolution",
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

async function checkEvolutionWorkflow(page, base) {
  const workspace = await page.evaluate(async () => {
    const config = await (await fetch("/api/config")).json()
    return config.agents.defaults.workspace
  })
  const legacyDir = path.join(workspace, "state", "evolution")
  const skill = "fixture-evolution-workflow"
  const skillPath = path.join(workspace, "skills", skill, "SKILL.md")
  await fs.mkdir(legacyDir, { recursive: true })
  await fs.writeFile(
    path.join(legacyDir, "pattern-records.jsonl"),
    JSON.stringify({
      id: "fixture-pattern",
      workspace_id: workspace,
      kind: "pattern",
      label: skill,
      summary: "Fixture evolution workflow",
      status: "ready",
      created_at: new Date().toISOString(),
    }) + "\n",
  )
  const response = await page.request.patch(`${base}/api/config`, {
    data: {
      evolution: { enabled: true, mode: "apply", cold_path_trigger: "manual" },
    },
  })
  if (!response.ok()) throw new Error(await response.text())
  const restart = await page.request.post(`${base}/api/gateway/restart`)
  if (!restart.ok()) throw new Error(await restart.text())
  const deadline = Date.now() + 15000
  for (;;) {
    const status = await page.request.get(`${base}/api/evolution`)
    if (status.ok() && (await status.json()).mode === "apply") break
    if (Date.now() > deadline)
      throw new Error(
        "Gateway did not activate the evolution configuration: " +
          (await status.text()),
      )
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  await page.goto(`${base}/config`)
  await page.getByRole("button", { name: /Evolution drafts|演化草稿/ }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: /Generate drafts|运行草拟/ }).click()
  await dialog.getByRole("button", { name: new RegExp(skill) }).click()
  await dialog
    .getByRole("button", { name: /Approve and publish|批准并发布/ })
    .waitFor()
  if (
    await fs.stat(skillPath).then(
      () => true,
      () => false,
    )
  )
    throw new Error("An unapproved candidate changed the live skill")
  await dialog
    .getByRole("button", { name: /Approve and publish|批准并发布/ })
    .click()
  await dialog
    .getByRole("button", { name: /Roll back version|回滚版本/ })
    .waitFor()
  if (!(await fs.readFile(skillPath, "utf8")).includes(`name: ${skill}`))
    throw new Error("Approved draft did not publish the workspace skill")
  await dialog
    .getByRole("button", { name: /Roll back version|回滚版本/ })
    .click()
  await dialog
    .getByRole("button", { name: /Confirm rollback|确认回滚/ })
    .click()
  await dialog.getByText(/Rolled back|已回滚/, { exact: true }).waitFor()
  if (
    await fs.stat(skillPath).then(
      () => true,
      () => false,
    )
  )
    throw new Error("Rollback did not remove the newly created skill")
  console.log(
    "Verified manual drafting from legacy state, approval, and rollback",
  )
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
