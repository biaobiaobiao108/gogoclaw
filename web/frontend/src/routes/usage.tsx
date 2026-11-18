import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/usage")({
  component: lazyRouteComponent(
    () => import("@/components/usage/usage-page"),
    "UsagePage",
  ),
})
