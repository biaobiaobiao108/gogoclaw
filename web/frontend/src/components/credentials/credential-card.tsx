import type { ReactNode } from "react"

import type { OAuthProviderStatus } from "@/api/oauth"

import { ProviderStatusLine } from "./provider-status-line"

interface CredentialCardProps {
  title: ReactNode
  description: string
  status: OAuthProviderStatus["status"]
  authMethod?: string
  details?: ReactNode
  actions: ReactNode
  footer?: ReactNode
}

export function CredentialCard({
  title,
  description,
  status,
  authMethod,
  details,
  actions,
  footer,
}: CredentialCardProps) {
  return (
    <section className="bg-card flex h-full min-w-0 flex-col gap-4 rounded-lg border p-4">
      <div className="space-y-1">
        <h3 className="text-base font-semibold">{title}</h3>
        <p className="text-muted-foreground mt-1 text-xs">{description}</p>
      </div>

      <ProviderStatusLine status={status} authMethod={authMethod} />
      {details && (
        <div className="text-muted-foreground text-xs leading-5">{details}</div>
      )}

      <div className="mt-auto flex flex-col gap-3 pt-2">
        <div>{actions}</div>
        {footer && <div>{footer}</div>}
      </div>
    </section>
  )
}
