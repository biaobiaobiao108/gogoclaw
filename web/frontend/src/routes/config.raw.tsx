import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/config/raw")({
  component: lazyRouteComponent(
    () => import("@/components/config/raw-config-page"),
    "RawConfigPage",
  ),
})
