const knownStatuses = new Set(["available", "unconfigured", "unreachable"])

export function modelStatusLabel(
  status: string,
  translate: (key: string) => string,
): string {
  if (!knownStatuses.has(status)) return translate("models.status.unknown")
  return translate(`models.status.${status}`)
}
