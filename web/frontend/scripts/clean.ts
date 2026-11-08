import { existsSync } from "node:fs"
import { rm } from "node:fs/promises"
import { resolve } from "node:path"

const frontendDir = resolve(import.meta.dirname, "..")
const backendDist = resolve(frontendDir, "../backend/dist")
const buildDir = resolve(frontendDir, "../build")
const frontendDist = resolve(frontendDir, "dist")

const targets = [frontendDist, backendDist, buildDir]

for (const target of targets) {
  if (existsSync(target)) {
    await rm(target, { recursive: true, force: true })
  }
}

const gitkeep = resolve(backendDist, ".gitkeep")
await Bun.write(
  gitkeep,
  "# Keep the embedded web backend dist directory in version control.\n",
)
