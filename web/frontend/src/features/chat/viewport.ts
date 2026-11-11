export interface SessionViewportSnapshot {
  scrollTop: number
  isAtBottom: boolean
  autoScrollEnabled: boolean
  hasScrolled: boolean
  expandedProcesses: Record<string, boolean>
}

const MAX_VIEWPORT_SESSIONS = 50
const MAX_CACHED_ROWS = 2000

const viewportCache = new Map<string, SessionViewportSnapshot>()
const rowSizeCache = new Map<string, number>()

export function getSessionViewport(
  sessionId: string,
): SessionViewportSnapshot | undefined {
  return viewportCache.get(sessionId)
}

export function saveSessionViewport(
  sessionId: string,
  snapshot: Partial<SessionViewportSnapshot>,
) {
  if (!sessionId) return
  const current = viewportCache.get(sessionId) ?? {
    scrollTop: 0,
    isAtBottom: true,
    autoScrollEnabled: true,
    hasScrolled: false,
    expandedProcesses: {},
  }

  const next: SessionViewportSnapshot = {
    ...current,
    ...snapshot,
    expandedProcesses: snapshot.expandedProcesses
      ? { ...snapshot.expandedProcesses }
      : current.expandedProcesses,
  }

  // Maintain cache size
  if (
    !viewportCache.has(sessionId) &&
    viewportCache.size >= MAX_VIEWPORT_SESSIONS
  ) {
    const oldestKey = viewportCache.keys().next().value
    if (oldestKey) viewportCache.delete(oldestKey)
  }

  viewportCache.set(sessionId, next)
}

export function clearSessionViewport(sessionId: string) {
  viewportCache.delete(sessionId)
}

export function getCachedRowSize(key: string, fallback: number): number {
  const cached = rowSizeCache.get(key)
  return cached && cached > 0 ? cached : fallback
}

export function setCachedRowSize(key: string, size: number) {
  if (!key || size <= 0) return
  if (!rowSizeCache.has(key) && rowSizeCache.size >= MAX_CACHED_ROWS) {
    const oldestKey = rowSizeCache.keys().next().value
    if (oldestKey) rowSizeCache.delete(oldestKey)
  }
  rowSizeCache.set(key, Math.round(size))
}

export function clearCachedRowSizes() {
  rowSizeCache.clear()
}
