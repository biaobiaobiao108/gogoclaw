const INTERACTIVE_SELECTOR =
  'button:not(:disabled), a[href], summary, [role="button"]:not([aria-disabled="true"])'
const MIN_PRESS_MS = 90

let activeTarget: HTMLElement | null = null
let pressedAt = 0
let releaseTimer: number | undefined

function findTarget(target: EventTarget | null): HTMLElement | null {
  return target instanceof Element
    ? target.closest<HTMLElement>(INTERACTIVE_SELECTOR)
    : null
}

function startPress(target: HTMLElement, clientX?: number, clientY?: number) {
  if (activeTarget && activeTarget !== target) releasePress(activeTarget)
  window.clearTimeout(releaseTimer)
  activeTarget = target
  pressedAt = performance.now()

  if (clientX !== undefined && clientY !== undefined) {
    const rect = target.getBoundingClientRect()
    const x = rect.width > 0 ? ((clientX - rect.left) / rect.width) * 100 : 50
    const y = rect.height > 0 ? ((clientY - rect.top) / rect.height) * 100 : 50
    target.style.setProperty("--press-x", `${Math.max(0, Math.min(100, x))}%`)
    target.style.setProperty("--press-y", `${Math.max(0, Math.min(100, y))}%`)
  }
  target.dataset.pressed = "true"
}

function releasePress(target = activeTarget) {
  if (!target || target !== activeTarget) return
  const remaining = Math.max(0, MIN_PRESS_MS - (performance.now() - pressedAt))
  window.clearTimeout(releaseTimer)
  releaseTimer = window.setTimeout(() => {
    target.removeAttribute("data-pressed")
    target.style.removeProperty("--press-x")
    target.style.removeProperty("--press-y")
    if (activeTarget === target) activeTarget = null
  }, remaining)
}

export function installInteractionFeedback() {
  document.addEventListener(
    "pointerdown",
    (event) => {
      if (event.button !== 0) return
      const target = findTarget(event.target)
      if (target) startPress(target, event.clientX, event.clientY)
    },
    { capture: true, passive: true },
  )
  window.addEventListener("pointerup", () => releasePress(), { passive: true })
  window.addEventListener("pointercancel", () => releasePress(), {
    passive: true,
  })
  window.addEventListener("blur", () => releasePress())

  document.addEventListener(
    "keydown",
    (event) => {
      if (event.repeat || (event.key !== "Enter" && event.key !== " ")) return
      const target = document.activeElement
      if (
        target instanceof HTMLElement &&
        target.matches(INTERACTIVE_SELECTOR)
      ) {
        startPress(target)
      }
    },
    true,
  )
  document.addEventListener(
    "keyup",
    (event) => {
      if (event.key === "Enter" || event.key === " ") releasePress()
    },
    true,
  )
}
