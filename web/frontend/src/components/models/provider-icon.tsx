import type { ProviderCatalogEntry } from "./provider-registry"

interface ProviderIconProps {
  provider: Pick<ProviderCatalogEntry, "key" | "label" | "iconSlug" | "domain">
}

export function ProviderIcon({ provider }: ProviderIconProps) {
  const initial = provider.label.trim().charAt(0).toUpperCase() || "?"
  return (
    <span
      className="border-border bg-muted text-foreground inline-flex size-4 shrink-0 items-center justify-center rounded-sm border text-[9px] font-semibold"
      aria-hidden="true"
    >
      {initial}
    </span>
  )
}
