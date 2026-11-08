const LAST_SESSION_STORAGE_KEY = "gogoclaw:last-session-id"
const SESSION_QUERY_KEY = "session"
const UNIX_MS_THRESHOLD = 1e12

function readStorageValue() {
  return (
    globalThis.localStorage?.getItem(LAST_SESSION_STORAGE_KEY)?.trim() || ""
  )
}

function readSessionQueryValue() {
  if (typeof window === "undefined") return ""
  const value = new URLSearchParams(window.location.search)
    .get(SESSION_QUERY_KEY)
    ?.trim()
  if (!value || value.length > 128) return ""
  return value
}

function writeSessionQueryValue(sessionId: string) {
  if (typeof window === "undefined") return
  if (
    window.location.pathname !== "/" &&
    !window.location.pathname.startsWith("/chat")
  )
    return
  const url = new URL(window.location.href)
  if (sessionId) {
    url.searchParams.set(SESSION_QUERY_KEY, sessionId)
  } else {
    url.searchParams.delete(SESSION_QUERY_KEY)
  }
  window.history.replaceState(window.history.state, "", url)
}

export function readStoredSessionId(): string {
  return readStorageValue()
}

export function writeStoredSessionId(sessionId: string) {
  if (sessionId) {
    globalThis.localStorage?.setItem(LAST_SESSION_STORAGE_KEY, sessionId)
    writeSessionQueryValue(sessionId)
    return
  }

  globalThis.localStorage?.removeItem(LAST_SESSION_STORAGE_KEY)
  writeSessionQueryValue("")
}

export function clearStoredSessionId() {
  globalThis.localStorage?.removeItem(LAST_SESSION_STORAGE_KEY)
}

export function generateSessionId(): string {
  const webCrypto = globalThis.crypto
  if (webCrypto && typeof webCrypto.randomUUID === "function") {
    return webCrypto.randomUUID()
  }

  if (webCrypto && typeof webCrypto.getRandomValues === "function") {
    const bytes = new Uint8Array(16)
    webCrypto.getRandomValues(bytes)

    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80

    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0"))
    return (
      `${hex[0]}${hex[1]}${hex[2]}${hex[3]}-` +
      `${hex[4]}${hex[5]}-` +
      `${hex[6]}${hex[7]}-` +
      `${hex[8]}${hex[9]}-` +
      `${hex[10]}${hex[11]}${hex[12]}${hex[13]}${hex[14]}${hex[15]}`
    )
  }

  return `session-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`
}

export function getInitialActiveSessionId(): string {
  const querySessionId = readSessionQueryValue()
  if (querySessionId) {
    writeStoredSessionId(querySessionId)
    return querySessionId
  }
  return readStorageValue() || generateSessionId()
}

export function normalizeUnixTimestamp(timestamp: number): number {
  return timestamp < UNIX_MS_THRESHOLD ? timestamp * 1000 : timestamp
}
