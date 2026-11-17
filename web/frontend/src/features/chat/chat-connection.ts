let currentGeneration = 0
let reconnectTimer: number | null = null
let reconnectAttempts = 0
let maintainConnection = false

export function connectionGeneration(): number {
  return currentGeneration
}

export function nextConnectionGeneration(): number {
  currentGeneration += 1
  clearReconnectTimer()
  return currentGeneration
}

export function invalidateConnectionGeneration(): number {
  currentGeneration += 1
  clearReconnectTimer()
  return currentGeneration
}

export function setMaintainConnection(value: boolean) {
  maintainConnection = value
}

export function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

export function reconnectAttemptCount(): number {
  return reconnectAttempts
}

export function resetReconnectAttempts(): number {
  const previous = reconnectAttempts
  reconnectAttempts = 0
  return previous
}

export function scheduleReconnect(
  generation: number,
  sessionId: string,
  isCurrentSession: (sessionId: string) => boolean,
  isGatewayRunning: () => boolean,
  connect: () => void,
) {
  if (
    !maintainConnection ||
    generation !== currentGeneration ||
    !isCurrentSession(sessionId) ||
    !isGatewayRunning() ||
    reconnectTimer !== null
  ) {
    return
  }

  const delay = Math.min(1000 * 2 ** reconnectAttempts, 5000)
  reconnectAttempts += 1
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    if (
      !maintainConnection ||
      generation !== currentGeneration ||
      !isCurrentSession(sessionId) ||
      !isGatewayRunning()
    ) {
      return
    }
    connect()
  }, delay)
}
