import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/logs")({
  component: lazyRouteComponent(
    () => import("@/components/logs/logs-page"),
    "LogsPage",
  ),
})
