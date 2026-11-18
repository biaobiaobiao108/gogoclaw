import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/credentials")({
  component: lazyRouteComponent(
    () => import("@/components/credentials/credentials-page"),
    "CredentialsPage",
  ),
})
