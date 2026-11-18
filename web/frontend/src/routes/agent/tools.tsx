import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/agent/tools")({
  component: AgentToolsRoute,
})

function AgentToolsRoute() {
  return <ToolsPageLazy />
}

const ToolsPageLazy = lazyRouteComponent(
  () => import("@/components/agent/tools/tools-page"),
  "ToolsPage",
)
