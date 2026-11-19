const storageKey = "gogoclaw:first-use"

function readFirstUse() {
  try {
    return localStorage.getItem(storageKey)
  } catch {
    return null
  }
}

export function isFirstUse() {
  return readFirstUse() === "pending"
}

export function hasFirstUseState() {
  return readFirstUse() !== null
}

export function setFirstUse(pending: boolean) {
  try {
    localStorage.setItem(storageKey, pending ? "pending" : "completed")
  } catch {
    // The guide also works with browser storage unavailable.
  }
}
