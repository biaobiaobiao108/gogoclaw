// Run against the local Bun dev server with a globally installed Playwright.
const fs = require("node:fs")
const playwright = require(process.env.PLAYWRIGHT_MODULE)

const browserName = process.env.GOGOCLAW_BROWSER || "chromium"
const browserType = playwright[browserName]
if (!browserType) throw new Error(`Unknown browser: ${browserName}`)

// WebKit reports cancelled same-origin loads from an outgoing document as page
// errors during Playwright-driven navigation. Playwright splits the message at
// the URL colon, so join the name and message before matching.
const refusedLoad =
  /^(?:Fetch API|XMLHttpRequest) cannot load (\S+) due to access control checks\.$/

function originOf(value) {
  try {
    return new URL(value).origin
  } catch {
    return undefined
  }
}

function isNavigationRefusedLoad(error, navigations) {
  if (browserName !== "webkit") return false
  const joined = `${error.name}:/${error.message}`
  const url = refusedLoad.exec(joined)?.[1]
  if (!url) return false
  const origin = originOf(url)
  return (
    origin !== undefined &&
    [...navigations].some((navigation) => originOf(navigation.from) === origin)
  )
}

async function main() {
  const browser = await browserType.launch({
    headless: true,
    ...(browserName === "chromium" && process.env.GOGOCLAW_BROWSER_EXECUTABLE
      ? { executablePath: process.env.GOGOCLAW_BROWSER_EXECUTABLE }
      : {}),
  })
  try {
    const page = await browser.newPage({
      viewport: { width: 1280, height: 800 },
    })
    const errors = []
    const api = {
      configPatches: [],
      channelPatches: [],
      imports: 0,
      uploads: 0,
      deletes: 0,
      logClears: 0,
      approvalResponse: null,
      gatewayStatus: "running",
      gatewayStarts: 0,
      sessionFixture: false,
      historyRequests: [],
    }
    const testNavigations = new Set()
    const navigate = async (url, options) => {
      const navigation = { from: page.url() }
      testNavigations.add(navigation)
      try {
        return await page.goto(url, options)
      } finally {
        testNavigations.delete(navigation)
      }
    }

    page.on("pageerror", (error) => {
      if (isNavigationRefusedLoad(error, testNavigations)) return
      errors.push(error.message)
    })
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text())
    })
    await page.addInitScript(() => {
      try {
        localStorage.setItem("i18nextLng", "zh-CN")
        localStorage.setItem(
          "gogoclaw-tour-state",
          JSON.stringify({ currentStep: "completed", isActive: false }),
        )
      } catch {}
    })
    if (process.env.AXE_MODULE) {
      if (!fs.existsSync(process.env.AXE_MODULE))
        throw new Error(`AXE_MODULE does not exist: ${process.env.AXE_MODULE}`)
      await page.addInitScript({ path: process.env.AXE_MODULE })
    }

    await page.routeWebSocket("**/pico/ws*", (socket) => {
      socket.onMessage((raw) => {
        const message = JSON.parse(raw)
        if (message.type === "message.send") {
          setTimeout(() => {
            socket.send(
              JSON.stringify({
                type: "approval.request",
                payload: {
                  approval_id: "approval-1",
                  tool: "exec",
                  arguments: { command: "printf public-risk" },
                  risk_summary: "Runs a command in the workspace.",
                },
              }),
            )
          }, 50)
        }
        if (message.type === "approval.respond") {
          api.approvalResponse = message.payload
          socket.send(
            JSON.stringify({
              type: "approval.resolved",
              payload: { approval_id: message.payload.approval_id },
            }),
          )
        }
      })
    })

    await page.route("**/api/**", async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname
      const method = request.method()
      if (!path.startsWith("/api/")) return route.continue()
      let status = 200
      let body = {}

      if (path === "/api/auth/status") {
        body = { initialized: true, authenticated: true }
      } else if (path === "/api/gateway/status") {
        body = {
          gateway_status: api.gatewayStatus,
          gateway_start_allowed: true,
          gateway_restart_required: false,
        }
      } else if (path === "/api/gateway/readiness") {
        body = {
          status: "ready",
          config_state: "applied",
          checks: {
            reload: {
              status: "pass",
              timestamp: "2026-11-19T00:00:00Z",
            },
          },
        }
      } else if (path === "/api/gateway/start" && method === "POST") {
        api.gatewayStarts++
        api.gatewayStatus = "running"
        body = { status: "ok" }
      } else if (path === "/api/gateway/logs" && method === "GET") {
        body = {
          logs: ["[INFO] gateway ready", "[WARN] public exposure enabled"],
          log_total: 2,
          log_run_id: 7,
        }
      } else if (path === "/api/gateway/logs/clear") {
        api.logClears++
        body = { log_total: 0, log_run_id: 8 }
      } else if (path === "/api/config" && method === "GET") {
        body = configResponse()
      } else if (path === "/api/config" && method === "PATCH") {
        const patch = JSON.parse(request.postData() || "{}")
        if (patch.channel_list) api.channelPatches.push(patch)
        else api.configPatches.push(patch)
        body = { status: "ok" }
      } else if (path === "/api/system/launcher-config") {
        body = {
          port: 18800,
          public: true,
          allowed_cidrs: [],
          allow_localhost_bypass: true,
        }
      } else if (path === "/api/system/version") {
        body = {
          version: "test",
          git_commit: "abcdef12",
          build_time: "2026-11-17T00:00:00Z",
          go_version: "go1.25",
        }
      } else if (path === "/api/channels/catalog") {
        body = {
          channels: [
            { name: "pico", display_name: "Pico", config_key: "pico" },
          ],
        }
      } else if (path === "/api/channels/pico/config") {
        body = {
          config: {
            enabled: false,
            type: "pico",
            allow_from: ["trusted-user"],
            settings: { token: "configured" },
          },
          configured_secrets: ["token"],
          config_key: "pico",
        }
      } else if (path === "/api/skills" && method === "GET") {
        body = {
          skills: [
            {
              name: "release-check",
              path: "skills/release-check/SKILL.md",
              source: "workspace",
              description: "Checks release readiness.",
              origin_kind: "manual",
            },
          ],
        }
      } else if (path === "/api/skills/import" && method === "POST") {
        api.imports++
        body = {
          status: "ok",
          name: "imported-skill",
          path: "skills/imported-skill/SKILL.md",
          source: "workspace",
          description: "Imported in browser smoke.",
          origin_kind: "manual",
        }
      } else if (path.startsWith("/api/skills/") && method === "GET") {
        const name = decodeURIComponent(path.split("/").pop())
        body = {
          name,
          path: `skills/${name}/SKILL.md`,
          source: "workspace",
          description: "Skill detail.",
          origin_kind: "manual",
          content: "# Skill detail",
        }
      } else if (path === "/api/session-management") {
        body = {
          items: api.deletes
            ? []
            : [
                {
                  id: "session-1",
                  key: "agent:main:telegram:user-1",
                  count: 2,
                  updated: "2026-11-17T05:00:00Z",
                  scope: {
                    channel: "telegram",
                    agent_id: "main",
                    values: { sender: "user-1" },
                  },
                },
              ],
        }
      } else if (
        path === "/api/session-management/session-1" &&
        method === "GET"
      ) {
        body = {
          messages: [
            { role: "user", content: "Check the release." },
            { role: "assistant", content: "Checks completed." },
          ],
          next_cursor: 0,
          has_more: false,
          deliveries: [
            {
              id: "accepted-1",
              channel: "telegram",
              status: "acknowledged",
              updated_at: "2026-11-17T05:00:00Z",
            },
            {
              id: "unknown-1",
              channel: "telegram",
              status: "delivery_unknown",
              updated_at: "2026-11-17T05:00:10Z",
            },
          ],
        }
      } else if (
        path === "/api/session-management/session-1" &&
        method === "DELETE"
      ) {
        api.deletes++
        status = 204
      } else if (path === "/api/uploads/config") {
        body = { max_media_size: 8 }
      } else if (path === "/api/uploads" && method === "POST") {
        api.uploads++
        const multipart = request.postDataBuffer()
        const filename =
          multipart?.toString("utf8").match(/filename="([^"]+)"/)?.[1] ||
          "note.txt"
        body = {
          id: `upload-${api.uploads}`,
          type: "file",
          url: `/api/attachments/session/upload-${api.uploads}`,
          filename,
          content_type: "text/plain",
        }
      } else if (path === "/api/uploads" && method === "DELETE") {
        status = 204
      } else if (path === "/api/models") {
        body = modelResponse()
      } else if (path === "/api/usage") {
        body = usageResponse()
      } else if (path === "/api/sessions") {
        body = { items: [], has_more: false }
        if (api.sessionFixture) {
          const params = new URL(route.request().url()).searchParams
          const projectID = params.get("project_id")
          const offset = Number(params.get("cursor") || 0)
          const limit = Number(params.get("limit"))
          api.historyRequests.push(projectID)
          if (!params.has("archived")) {
            const items =
              projectID === "none" ? historyItems : [projectHistoryItem]
            const end = offset + limit
            body = {
              items: items.slice(offset, end),
              has_more: end < items.length,
              next_cursor: end < items.length ? String(end) : undefined,
            }
          }
        }
      } else if (path === "/api/projects") {
        if (api.sessionFixture) {
          api.historyRequests.push("projects")
          body = [{ id: "project-a", name: "项目 A", instructions: "" }]
        } else {
          body = []
        }
      } else if (path === "/api/sessions/search" && api.sessionFixture) {
        body = [projectHistoryItem]
      } else if (path.startsWith("/api/sessions/")) {
        body = { messages: [], has_more: false }
        const id = path.split("/").at(-1)
        const session = [projectHistoryItem, ...historyItems].find(
          (item) => item.id === id,
        )
        if (api.sessionFixture && session) {
          await new Promise((resolve) =>
            setTimeout(resolve, id === "history-29" ? 650 : 140),
          )
          body = {
            title: session.title,
            project_id: session.project_id,
            messages: [
              { role: "assistant", content: `回复 ${id}`, timestamp: 1 },
            ],
            has_more: false,
          }
        }
      } else {
        status = 501
        body = { error: `missing browser mock: ${method} ${path}` }
        errors.push(body.error)
      }

      if (status === 204) {
        await route.fulfill({ status })
      } else {
        await route.fulfill({
          status,
          contentType: "application/json",
          body: JSON.stringify(body),
        })
      }
    })

    const base = process.env.GOGOCLAW_BROWSER_URL || "http://127.0.0.1:5173"
    await navigate(`${base}/chat`)
    await page
      .locator('textarea:not([aria-hidden="true"])')
      .waitFor()
      .catch(async () => {
        throw new Error(
          `Chat composer missing at ${page.url()}: ${JSON.stringify({
            body: (await page.locator("body").innerText()).slice(0, 1200),
            errors,
          })}`,
        )
      })

    const composer = page.locator('textarea:not([aria-hidden="true"])')
    const attach = page.getByRole("button", { name: "添加附件" })
    let chooserPromise = page.waitForEvent("filechooser")
    await attach.click()
    let chooser = await chooserPromise
    await chooser.setFiles({
      name: "too-large.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("0123456789"),
    })
    const tooLargeToast = page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "too-large.txt" })
    await tooLargeToast.waitFor()
    await page.mouse.move(0, 0)
    await tooLargeToast.waitFor({ state: "detached" })

    chooserPromise = page.waitForEvent("filechooser")
    await attach.click()
    chooser = await chooserPromise
    await chooser.setFiles({
      name: "note.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("abc"),
    })
    await page.getByText("note.txt", { exact: true }).waitFor()
    if (api.uploads !== 1)
      throw new Error("Attachment picker upload did not run")

    const dataTransfer = await page.evaluateHandle(() => {
      const transfer = new DataTransfer()
      transfer.items.add(
        new File(["drop"], "dropped.txt", { type: "text/plain" }),
      )
      return transfer
    })
    const dropzone = page.locator("[data-chat-dropzone]")
    await dropzone.dispatchEvent("dragenter", { dataTransfer })
    await dropzone.dispatchEvent("dragover", { dataTransfer })
    await dropzone.dispatchEvent("drop", { dataTransfer })
    await page.getByText("dropped.txt", { exact: true }).waitFor()
    if (api.uploads !== 2)
      throw new Error("Attachment drag/drop upload did not run")

    await composer.fill("Run the release check")
    await composer.press("Enter")
    const approval = page.getByRole("region", { name: "需要批准工具调用" })
    await approval.waitFor()
    await approval.getByRole("button", { name: "批准一次" }).click()
    await approval.waitFor({ state: "detached" })
    if (api.approvalResponse?.approve !== true)
      throw new Error("Approval response was not sent")
    await scan(page, "chat", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "chat")

    await navigate(`${base}/config`)
    const approvalSwitch = page.getByRole("switch", { name: "工具调用审批" })
    await approvalSwitch.waitFor()
    await approvalSwitch.focus()
    await page.keyboard.press("Space")
    const configSaved = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/config") &&
        response.request().method() === "PATCH",
    )
    await page.getByRole("button", { name: "保存", exact: true }).click()
    await configSaved
    if (api.configPatches.length !== 1)
      throw new Error("Config save did not emit one PATCH")
    if (api.configPatches[0].tools?.approval?.enabled !== true)
      throw new Error("Config save omitted the approval change")
    await scan(page, "config", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "config")

    await navigate(`${base}/channels/pico`)
    const channelSwitch = page.getByRole("switch", { name: "启用频道" })
    await channelSwitch.waitFor()
    await channelSwitch.click()
    const channelSaved = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/config") &&
        response.request().method() === "PATCH",
    )
    await page.getByRole("button", { name: "保存", exact: true }).click()
    await channelSaved
    if (api.channelPatches.length !== 1)
      throw new Error("Channel save did not emit one PATCH")
    if (!api.channelPatches[0].channel_list?.pico)
      throw new Error("Channel save omitted the Pico payload")
    await scan(page, "channel", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "channel")

    await navigate(`${base}/agent/skills`)
    await page.getByText("release-check", { exact: true }).waitFor()
    const importButton = page.getByRole("button", { name: "导入技能" })
    await importButton.click()
    const importDialog = page.getByRole("dialog", { name: "导入到工作区" })
    await importDialog.waitFor()
    await page.keyboard.press("Escape")
    await importDialog.waitFor({ state: "detached" })
    if (
      (await page.evaluate(() => document.activeElement?.textContent)) !==
      "导入技能"
    )
      throw new Error("Skill import dialog did not restore focus")

    await importButton.click()
    const importInput = page.locator('input[type="file"][accept*=".zip"]')
    await importInput.setInputFiles({
      name: "unsafe.exe",
      mimeType: "application/octet-stream",
      buffer: Buffer.from("no"),
    })
    await page.getByText("仅支持导入 Markdown 或 ZIP 技能文件。").waitFor()
    await importInput.setInputFiles({
      name: "imported.md",
      mimeType: "text/markdown",
      buffer: Buffer.from("# imported"),
    })
    await importDialog.waitFor({ state: "detached" })
    if (api.imports !== 1)
      throw new Error("Skill import request count is wrong")
    await scan(page, "skills", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "skills")

    await navigate(`${base}/sessions`)
    await page.getByText("telegram · main", { exact: true }).first().waitFor()
    await page.getByText("telegram · main", { exact: true }).first().click()
    await page.getByText("Checks completed.", { exact: true }).waitFor()
    await page.getByRole("heading", { name: "最近投递" }).waitFor()
    await page.getByText("telegram · 平台已接收", { exact: true }).waitFor()
    await page
      .getByText("telegram · 平台接收状态未知", { exact: true })
      .waitFor()
    await page.getByRole("button", { name: "删除", exact: true }).click()
    const deleteDialog = page.getByRole("alertdialog")
    await deleteDialog.getByRole("button", { name: "删除" }).click()
    await page.getByText("没有找到会话", { exact: false }).waitFor()
    if (api.deletes !== 1) throw new Error("Session delete did not run")
    await scan(page, "sessions", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "sessions")

    await navigate(`${base}/logs`)
    await page.getByText("gateway ready", { exact: false }).waitFor()
    await page.getByRole("button", { name: "清空日志" }).click()
    await page.getByText("等待日志中", { exact: false }).waitFor()
    if (api.logClears !== 1) throw new Error("Log clear did not run")
    await scan(page, "logs", Boolean(process.env.AXE_MODULE))
    await assertNoRawKeys(page, "logs")

    await page.emulateMedia({ reducedMotion: "reduce" })
    await navigate(`${base}/config`)
    const reducedDuration = await page
      .getByRole("button", { name: "切换到深色主题" })
      .evaluate((element) => getComputedStyle(element).transitionDuration)
    if (Number.parseFloat(reducedDuration) > 0.0015)
      throw new Error(`Reduced-motion duration was ${reducedDuration}`)

    await page.emulateMedia({ reducedMotion: "no-preference" })
    await navigate(`${base}/config`)
    await page.setViewportSize({ width: 640, height: 800 })
    await page.addStyleTag({ content: "html { font-size: 200% !important; }" })
    const scaledType = await page.evaluate(() => ({
      root: Number.parseFloat(
        getComputedStyle(document.documentElement).fontSize,
      ),
      control: Number.parseFloat(
        getComputedStyle(
          document.querySelector('button[type="button"]') || document.body,
        ).fontSize,
      ),
    }))
    if (scaledType.root < 31.5 || scaledType.control < 24)
      throw new Error(
        `200% text did not scale typography: ${JSON.stringify(scaledType)}`,
      )
    if (
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth + 1,
      )
    )
      throw new Error("Config overflows at 200% text and 640px width")
    await page.getByRole("button", { name: "保存", exact: true }).waitFor()

    for (const width of [375, 320]) {
      await navigate(`${base}/config`)
      await page.setViewportSize({ width, height: 812 })
      await page.getByRole("button", { name: "保存", exact: true }).waitFor()
      if (
        await page.evaluate(
          () => document.documentElement.scrollWidth > innerWidth + 1,
        )
      )
        throw new Error(`Config overflows at ${width}px width`)
    }

    for (const width of [1280, 390]) {
      api.gatewayStatus = "stopped"
      await page.setViewportSize({ width, height: 844 })
      await navigate(`${base}/chat`)
      await page.getByRole("heading", { name: "服务尚未运行" }).waitFor()
      await page
        .locator("main")
        .getByRole("button", { name: "启动服务" })
        .click()
    }
    if (api.gatewayStarts !== 2)
      throw new Error("First-run start action did not reach gateway API")

    api.gatewayStatus = "error"
    await navigate(`${base}/chat`)
    await page.locator("main").getByRole("link", { name: /日志/ }).waitFor()

    api.gatewayStatus = "stopped"
    api.sessionFixture = true
    await page.setViewportSize({ width: 1280, height: 800 })
    await navigate(`${base}/chat`)
    await checkSessionSwitching(page, api)

    if (errors.length) throw new Error(`Browser errors: ${errors.join(", ")}`)
    console.log(
      `${browserName} console smoke passed: upload, approval, config, channel, skills, session switching, logs, focus, axe, text scaling and mobile reflow`,
    )
  } finally {
    await browser.close()
  }
}

const historyItems = Array.from({ length: 60 }, (_, index) => ({
  id: `history-${index}`,
  title: `历史会话 ${index}`,
  preview: "",
  message_count: 1,
  updated: "2025-01-01T00:00:00Z",
}))
const projectHistoryItem = {
  ...historyItems[0],
  id: "project-chat",
  title: "项目内会话",
  project_id: "project-a",
}

async function checkSessionSwitching(page, api) {
  const history = page
    .locator("main")
    .getByRole("navigation", { name: "历史记录" })
  const row = (index) =>
    history.getByRole("button", { name: `历史会话 ${index}`, exact: true })
  await row(0).click()
  await page.getByText("回复 history-0", { exact: true }).waitFor()
  await history.evaluate((element) => {
    element.scrollTop = 850
  })
  await row(28).waitFor()
  await page.waitForFunction(
    () =>
      document.querySelector('main nav[aria-label="历史记录"]').scrollTop >=
      800,
  )
  const offset = await history.evaluate((element) => element.scrollTop)
  const node = await row(27).elementHandle()
  const requests = api.historyRequests.length
  await row(29).click()
  await row(29).and(history.locator('[aria-busy="true"]')).waitFor()
  if (!(await page.getByText("回复 history-0", { exact: true }).isVisible()))
    throw new Error("Pending navigation replaced the current conversation")
  await row(28).click()
  await page.getByText("回复 history-28", { exact: true }).waitFor()
  await row(28).and(history.locator('[aria-current="page"]')).waitFor()
  await page.waitForTimeout(700)
  if (api.historyRequests.length !== requests)
    throw new Error(
      "Selecting a session requested the projects or session list",
    )
  if (!(await node.evaluate((element) => element.isConnected)))
    throw new Error("Session switch remounted an unrelated history row")
  if (
    Math.abs(
      (await history.evaluate((element) => element.scrollTop)) - offset,
    ) > 1
  )
    throw new Error("Session switch moved the history viewport")
  if (!(await page.getByText("回复 history-28", { exact: true }).isVisible()))
    throw new Error("A superseded request replaced the selected session")

  const search = page.locator("main").getByPlaceholder("搜索全部对话")
  await search.fill("项目内会话")
  await history.getByRole("button", { name: /项目内会话/ }).click()
  await page.getByText("回复 project-chat", { exact: true }).waitFor()
  await search.fill("")
  await page.waitForFunction(
    () =>
      document.querySelector('main nav[aria-label="历史记录"]').scrollTop >=
      800,
  )
  if (api.historyRequests.slice(requests).join(",") !== "project-a")
    throw new Error(
      "Opening an unloaded project did not fetch only that project",
    )

  await page.getByRole("button", { name: "显示归档对话" }).click()
  await history.getByText("暂无对话历史").first().waitFor()
  const archivedRequests = api.historyRequests.length
  await page.getByRole("button", { name: "显示当前对话" }).click()
  await row(28).waitFor()
  if (api.historyRequests.length !== archivedRequests)
    throw new Error("Returning from archive requested an already loaded group")
  if (
    Math.abs(
      (await history.evaluate((element) => element.scrollTop)) - offset,
    ) > 1
  )
    throw new Error("Returning from archive lost the active history viewport")

  if (process.env.GOGOCLAW_HISTORY_SCREENSHOT)
    await page.screenshot({ path: process.env.GOGOCLAW_HISTORY_SCREENSHOT })
  await page.setViewportSize({ width: 390, height: 844 })
  const openHistory = page
    .locator("main")
    .getByRole("button", { name: "历史记录", exact: true })
  const mobileHistory = page.getByRole("dialog", { name: "历史记录" })
  const mobileList = mobileHistory.getByRole("navigation", { name: "历史记录" })
  await openHistory.click()
  const mobileRow = mobileList.getByRole("button", {
    name: "历史会话 28",
    exact: true,
  })
  await mobileRow.waitFor()
  const mobileOffset = await mobileList.evaluate((element) => element.scrollTop)
  if (Math.abs(mobileOffset - offset) > 1)
    throw new Error(
      `Opening the mobile history moved from ${offset} to ${mobileOffset}`,
    )
  // Click the visible row directly so Playwright cannot scroll it into view.
  await mobileHistory.evaluate(async (element) => {
    await Promise.all(
      element.getAnimations().map((animation) => animation.finished),
    )
  })
  const bounds = await mobileRow.boundingBox()
  if (!bounds) throw new Error("The mobile history row was hidden")
  await page.mouse.click(
    bounds.x + bounds.width / 2,
    bounds.y + bounds.height / 2,
  )
  await mobileHistory.waitFor({ state: "hidden" })
  await page.getByText("回复 history-28", { exact: true }).waitFor()
  await openHistory.click()
  await mobileRow.and(mobileList.locator('[aria-current="page"]')).waitFor()
  const reopenedOffset = await mobileList.evaluate(
    (element) => element.scrollTop,
  )
  if (Math.abs(reopenedOffset - mobileOffset) > 1)
    throw new Error(
      `Reopening the mobile history moved from ${mobileOffset} to ${reopenedOffset}`,
    )
  if (api.historyRequests.length !== archivedRequests)
    throw new Error("Mobile session selection refetched the history list")
}

async function scan(page, label, enabled) {
  if (!enabled) return
  await page.waitForTimeout(500)
  const result = await page.evaluate(async () => {
    if (typeof axe === "undefined") throw new Error("axe is unavailable")
    return axe.run(document, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"],
      },
    })
  })
  if (result.violations.length > 0) {
    const details = result.violations
      .map((violation) => {
        const nodes = violation.nodes
          .map(
            (node) =>
              `${node.target.join(" ")} (${node.failureSummary}) ${node.html}`,
          )
          .join(" | ")
        return `${violation.id}: ${violation.help}: ${nodes}`
      })
      .join("; ")
    throw new Error(`axe violations on ${label}: ${details}`)
  }
}

async function assertNoRawKeys(page, label) {
  const body = await page.locator("body").innerText()
  const matches = body.match(
    /\b(?:chat|pages|models|security|sessionManagement|channels|common|navigation)\.[A-Za-z][A-Za-z0-9_.]*/g,
  )
  if (matches)
    throw new Error(`${label} rendered raw i18n keys: ${matches.join(", ")}`)
}

function configResponse() {
  return {
    agents: {
      defaults: {
        workspace: "/srv/gogoclaw",
        restrict_to_workspace: true,
        split_on_marker: false,
        max_tokens: 8192,
        context_window: 32768,
        max_tool_iterations: 50,
        summarize_message_threshold: 20,
        summarize_token_percent: 75,
        tool_feedback: {
          enabled: false,
          max_args_length: 300,
          separate_messages: false,
        },
        usage_governance: {
          session_token_budget: 0,
          daily_token_budget: 0,
          on_budget_exceeded: "stop",
        },
        turn_profile: {
          enabled: false,
          history: { mode: "default" },
          system_prompt: { mode: "default" },
          skills: { mode: "default" },
          tools: { mode: "default" },
        },
      },
    },
    session: { dm_scope: "per-channel-peer" },
    tools: {
      approval: { enabled: false },
      exec: {
        enabled: true,
        allow_remote: true,
        enable_deny_patterns: false,
        custom_deny_patterns: [],
        custom_allow_patterns: [],
        timeout_seconds: 0,
      },
      cron: { allow_command: true, exec_timeout_minutes: 5 },
      mcp: { enabled: false, servers: {} },
    },
    heartbeat: { enabled: true, interval: 30 },
    devices: { enabled: false, monitor_usb: true },
    evolution: { enabled: false, mode: "observe" },
  }
}

function modelResponse() {
  const zero = {
    calls: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cost_nanos: 0,
    cost_usd: 0,
  }
  return {
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
    usage: zero,
  }
}

function usageResponse() {
  return {
    days: 30,
    from: "2026-01-01",
    to: "2026-01-30",
    totals: {
      calls: 0,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      cost_nanos: 0,
      cost_usd: 0,
    },
    today: {
      calls: 0,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      cost_nanos: 0,
      cost_usd: 0,
    },
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

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
