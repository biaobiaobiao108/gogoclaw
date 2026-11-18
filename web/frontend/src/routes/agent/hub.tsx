import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/agent/hub")({
  component: AgentHubRoute,
})

function AgentHubRoute() {
  return <HubPageLazy />
}

const HubPageLazy = lazyRouteComponent(
  () => import("@/components/agent/hub/hub-page"),
  "HubPage",
)
