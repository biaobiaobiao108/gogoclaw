import { cp, mkdir, rm } from "node:fs/promises"
import { resolve } from "node:path"

const frontendDir = resolve(import.meta.dirname, "..")
const backendMode = process.argv.includes("--backend")
const outDir = resolve(frontendDir, backendMode ? "../backend/dist" : "dist")
const publicDir = resolve(frontendDir, "public")

function run(args: string[]) {
  const result = Bun.spawnSync({
    cmd: args,
    cwd: frontendDir,
    env: process.env,
    stdout: "inherit",
    stderr: "inherit",
  })
  if (result.exitCode !== 0) {
    throw new Error(
      `${args.join(" ")} failed with exit code ${result.exitCode}`,
    )
  }
}

await rm(outDir, { recursive: true, force: true })
run(["bun", "run", "routes"])
run(["bun", "run", "css"])
run([
  "bun",
  "build",
  "index.html",
  "--outdir",
  outDir,
  "--production",
  "--splitting",
  "--min-chunk-size=16384",
  "--public-path=/",
  "--chunk-naming=assets/[name]-[hash].[ext]",
  "--asset-naming=assets/[name]-[hash].[ext]",
])

await mkdir(outDir, { recursive: true })
await cp(publicDir, resolve(outDir, "public"), { recursive: true })

if (backendMode) {
  run(["bun", "./scripts/ensure-backend-gitkeep.ts"])
}

console.log(`Frontend built to ${outDir}`)
