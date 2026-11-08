const MIN_REVEAL_MS = 80
const MAX_REVEAL_MS = 420
const MS_PER_CHARACTER = 5

export function smoothRevealDuration(from: number, to: number): number {
  const delta = Math.max(0, to - from)
  return Math.min(
    MAX_REVEAL_MS,
    Math.max(MIN_REVEAL_MS, delta * MS_PER_CHARACTER),
  )
}

export function smoothRevealCount(
  from: number,
  to: number,
  progress: number,
): number {
  if (to <= from) return to
  const clamped = Math.min(1, Math.max(0, progress))
  const eased = 1 - (1 - clamped) ** 3
  return Math.min(to, from + Math.ceil((to - from) * eased))
}
