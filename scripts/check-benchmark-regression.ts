import { readFileSync } from "node:fs"

interface MetricSet {
  nsPerOp: number[]
  bytesPerOp: number[]
  allocsPerOp: number[]
}

const [baselinePath, currentPath] = process.argv.slice(2)
if (!baselinePath || !currentPath) {
  console.error(
    "usage: bun scripts/check-benchmark-regression.ts <baseline.txt> <current.txt>",
  )
  process.exit(2)
}

const maxTimeRatio = readRatio("GOGOCLAW_BENCH_MAX_TIME_RATIO", 1.35)
const maxAllocRatio = readRatio("GOGOCLAW_BENCH_MAX_ALLOC_RATIO", 1.25)
const minTimeDeltaNs = readRatio("GOGOCLAW_BENCH_MIN_TIME_DELTA_NS", 50_000)
const baseline = parseBenchmarks(readFileSync(baselinePath, "utf8"))
const current = parseBenchmarks(readFileSync(currentPath, "utf8"))
const regressions: string[] = []
let compared = 0

for (const [name, currentMetrics] of current) {
  const baselineMetrics = baseline.get(name)
  if (!baselineMetrics) continue
  compared++
  compareMetric(
    name,
    "ns/op",
    median(baselineMetrics.nsPerOp),
    median(currentMetrics.nsPerOp),
    maxTimeRatio,
    minTimeDeltaNs,
  )
  compareMetric(
    name,
    "B/op",
    median(baselineMetrics.bytesPerOp),
    median(currentMetrics.bytesPerOp),
    maxAllocRatio,
    1,
  )
  compareMetric(
    name,
    "allocs/op",
    median(baselineMetrics.allocsPerOp),
    median(currentMetrics.allocsPerOp),
    maxAllocRatio,
    1,
  )
}

if (compared === 0) {
  console.error("No overlapping benchmark names were found")
  process.exit(1)
}
if (regressions.length > 0) {
  console.error(`Benchmark regressions detected (${compared} compared):`)
  for (const regression of regressions) console.error(`- ${regression}`)
  process.exit(1)
}
console.log(`Benchmark regression check passed (${compared} benchmarks compared)`)

function parseBenchmarks(content: string): Map<string, MetricSet> {
  const benchmarks = new Map<string, MetricSet>()
  const linePattern =
    /^(Benchmark\S+)\s+\d+\s+([\d.]+)\s+ns\/op(?:\s+([\d.]+)\s+B\/op)?(?:\s+([\d.]+)\s+allocs\/op)?/gm
  for (const match of content.matchAll(linePattern)) {
    let metrics = benchmarks.get(match[1])
    if (!metrics) {
      metrics = { nsPerOp: [], bytesPerOp: [], allocsPerOp: [] }
      benchmarks.set(match[1], metrics)
    }
    metrics.nsPerOp.push(Number(match[2]))
    if (match[3]) metrics.bytesPerOp.push(Number(match[3]))
    if (match[4]) metrics.allocsPerOp.push(Number(match[4]))
  }
  return benchmarks
}

function compareMetric(
  name: string,
  metric: string,
  baselineValue: number,
  currentValue: number,
  maxRatio: number,
  minDelta: number,
) {
  if (!Number.isFinite(baselineValue) || !Number.isFinite(currentValue)) return
  const delta = currentValue - baselineValue
  if (delta < minDelta || currentValue <= baselineValue * maxRatio) return
  regressions.push(
    `${name} ${metric}: ${format(baselineValue)} -> ${format(currentValue)} (${(
      (currentValue / baselineValue) * 100
    ).toFixed(1)}%)`,
  )
}

function median(values: number[]): number {
  if (values.length === 0) return Number.NaN
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 === 0
    ? (sorted[middle - 1] + sorted[middle]) / 2
    : sorted[middle]
}

function readRatio(name: string, fallback: number): number {
  const value = Number(process.env[name] ?? fallback)
  return Number.isFinite(value) && value > 0 ? value : fallback
}

function format(value: number): string {
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(
    value,
  )
}
