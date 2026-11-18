import { watch } from "node:fs"
import { resolve } from "node:path"

type SocketData = {
  headers: Headers
  pending: Array<string | Uint8Array>
  target: string
  upstream: WebSocket | null
}

const frontendDir = resolve(import.meta.dirname, "..")
const publicDir = resolve(frontendDir, "public")
const srcDir = resolve(frontendDir, "src")
const cacheDir = resolve(frontendDir, ".cache")
const hostname = argumentValue("--host") ?? "127.0.0.1"
const port = Number(argumentValue("--port") ?? 5173)
const backend = new URL(
  process.env.GOGOCLAW_BACKEND_URL ?? "http://localhost:18800",
)
const reloadClients = new Set<ReadableStreamDefaultController<Uint8Array>>()
const reloadEncoder = new TextEncoder()

function argumentValue(flag: string) {
  const index = process.argv.indexOf(flag)
  if (index === -1) {
    return undefined
  }
  return process.argv[index + 1]
}

function runScript(script: string) {
  const result = Bun.spawnSync({
    cmd: ["bun", "run", script],
    cwd: frontendDir,
    env: process.env,
    stdout: "inherit",
    stderr: "inherit",
  })
  if (result.exitCode !== 0) {
    throw new Error(
      `bun run ${script} failed with exit code ${result.exitCode}`,
    )
  }
}

function proxyHeaders(req: Request) {
  const headers = new Headers(req.headers)
  headers.set("host", new URL(req.url).host)
  return headers
}

async function proxyRequest(req: Request) {
  const incoming = new URL(req.url)
  const target = new URL(backend)
  target.pathname = incoming.pathname
  target.search = incoming.search

  try {
    return await fetch(target, {
      method: req.method,
      headers: proxyHeaders(req),
      body:
        req.method === "GET" || req.method === "HEAD" ? undefined : req.body,
      redirect: "manual",
    })
  } catch (error) {
    return Response.json(
      { error: `backend proxy failed: ${String(error)}` },
      { status: 502 },
    )
  }
}

function upgradeWebSocket(
  req: Request,
  server: Bun.Server<SocketData>,
): Response | undefined {
  if (req.headers.get("upgrade")?.toLowerCase() !== "websocket") {
    return new Response("Expected WebSocket upgrade", { status: 426 })
  }

  const incoming = new URL(req.url)
  const target = new URL(backend)
  target.protocol = backend.protocol === "https:" ? "wss:" : "ws:"
  target.pathname = incoming.pathname
  target.search = incoming.search

  const headers = proxyHeaders(req)
  for (const name of [
    "connection",
    "sec-websocket-accept",
    "sec-websocket-extensions",
    "sec-websocket-key",
    "sec-websocket-protocol",
    "sec-websocket-version",
    "upgrade",
  ]) {
    headers.delete(name)
  }

  const upgraded = server.upgrade(req, {
    data: {
      headers,
      pending: [],
      target: target.toString(),
      upstream: null,
    },
  })
  if (!upgraded) {
    return new Response("WebSocket upgrade failed", { status: 400 })
  }
}

function sendToUpstream(
  socket: Bun.ServerWebSocket<SocketData>,
  data: string | Uint8Array,
) {
  const upstream = socket.data.upstream
  if (upstream?.readyState === WebSocket.OPEN) {
    upstream.send(data)
    return
  }
  socket.data.pending.push(data)
}

function safeCloseCode(code: number) {
  return code === 1000 || (code >= 3000 && code <= 4999) ? code : 1000
}

function subscribeToReloads() {
  let client: ReadableStreamDefaultController<Uint8Array> | undefined
  return new Response(
    new ReadableStream<Uint8Array>({
      start(controller) {
        client = controller
        reloadClients.add(controller)
        controller.enqueue(reloadEncoder.encode(": connected\n\n"))
      },
      cancel() {
        if (client) {
          reloadClients.delete(client)
        }
      },
    }),
    {
      headers: {
        "cache-control": "no-cache",
        connection: "keep-alive",
        "content-type": "text/event-stream",
      },
    },
  )
}

let reloadTimer: ReturnType<typeof setTimeout> | undefined
function scheduleReload() {
  clearTimeout(reloadTimer)
  reloadTimer = setTimeout(() => {
    for (const controller of reloadClients) {
      try {
        controller.enqueue(reloadEncoder.encode("event: reload\ndata: 1\n\n"))
      } catch {
        reloadClients.delete(controller)
      }
    }
  }, 100)
}

runScript("routes")
runScript("css")

const app = (await import("../index.html")).default
const publicRoutes: Record<string, Bun.BunFile> = {}

for await (const file of new Bun.Glob("**/*").scan({ cwd: publicDir })) {
  const urlPath = `/public/${file.replaceAll("\\", "/")}`
  publicRoutes[urlPath] = Bun.file(resolve(publicDir, file))
}

const routes: Bun.Serve.RoutesWithUpgrade<SocketData, string> = {
  ...publicRoutes,
  "/api": proxyRequest,
  "/api/*": proxyRequest,
  "/pico/media": proxyRequest,
  "/pico/media/*": proxyRequest,
  "/pico/ws": upgradeWebSocket,
  "/__gogoclaw_reload": subscribeToReloads,
  "/*": app,
}

const server = Bun.serve<SocketData>({
  hostname,
  port,
  routes,
  development: { hmr: false, console: true },
  websocket: {
    open(socket) {
      const upstream = new WebSocket(socket.data.target, {
        headers: socket.data.headers,
      })
      socket.data.upstream = upstream

      upstream.onopen = () => {
        for (const data of socket.data.pending) {
          upstream.send(data)
        }
        socket.data.pending = []
      }
      upstream.onmessage = (event) => {
        socket.send(event.data)
      }
      upstream.onclose = (event) => {
        socket.close(safeCloseCode(event.code), event.reason)
      }
      upstream.onerror = () => {
        socket.close(1011, "backend websocket error")
      }
    },
    message(socket, message) {
      sendToUpstream(socket, message)
    },
    close(socket, code, reason) {
      socket.data.upstream?.close(safeCloseCode(code), reason)
    },
  },
})

const watchers = [
  Bun.spawn({
    cmd: ["bun", "run", "routes:watch"],
    cwd: frontendDir,
    env: process.env,
    stdout: "inherit",
    stderr: "inherit",
  }),
  Bun.spawn({
    cmd: ["bun", "run", "css:watch"],
    cwd: frontendDir,
    env: process.env,
    stdout: "inherit",
    stderr: "inherit",
  }),
]

await Bun.sleep(500)
const filesystemWatchers = [
  watch(frontendDir, (_event, filename) => {
    if (filename?.toString() === "index.html") {
      scheduleReload()
    }
  }),
  watch(srcDir, { recursive: true }, scheduleReload),
  watch(publicDir, { recursive: true }, scheduleReload),
  watch(cacheDir, (_event, filename) => {
    if (!filename || filename.toString() === "tailwind.css") {
      scheduleReload()
    }
  }),
]

console.log(`Frontend dev server: http://${hostname}:${port}`)
console.log(`Backend proxy: ${backend.origin}`)

let stopping = false
async function shutdown() {
  if (stopping) {
    return
  }
  stopping = true
  for (const watcher of watchers) {
    watcher.kill()
  }
  for (const watcher of filesystemWatchers) {
    watcher.close()
  }
  clearTimeout(reloadTimer)
  await server.stop(true)
}

process.once("SIGINT", shutdown)
process.once("SIGTERM", shutdown)
