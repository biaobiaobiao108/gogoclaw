import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/models")({
  validateSearch: (search: Record<string, unknown>): { setup?: true } => ({
    setup: search.setup === true ? (true as const) : undefined,
  }),
  component: lazyRouteComponent(
    () => import("@/components/models/models-page"),
    "ModelsPage",
  ),
})
