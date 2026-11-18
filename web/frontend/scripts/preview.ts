import { extname, isAbsolute, relative, resolve } from "node:path"

const frontendDir = resolve(import.meta.dirname, "..")
const distDir = resolve(frontendDir, "dist")
const hostname = argumentValue("--host") ?? "127.0.0.1"
const port = Number(argumentValue("--port") ?? 4173)
const index = Bun.file(resolve(distDir, "index.html"))

function argumentValue(flag: string) {
  const index = process.argv.indexOf(flag)
  if (index === -1) {
    return undefined
  }
  return process.argv[index + 1]
}

const server = Bun.serve({
  hostname,
  port,
  async fetch(req) {
    const pathname = decodeURIComponent(new URL(req.url).pathname)
    const target = resolve(distDir, `.${pathname}`)
    const relativeTarget = relative(distDir, target)
    if (relativeTarget.startsWith("..") || isAbsolute(relativeTarget)) {
      return new Response("Not found", { status: 404 })
    }

    const file = Bun.file(target)
    if (await file.exists()) {
      return new Response(file)
    }
    if (extname(pathname)) {
      return new Response("Not found", { status: 404 })
    }
    return new Response(index)
  },
})

console.log(`Frontend preview: ${server.url}`)
