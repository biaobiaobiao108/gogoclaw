import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/agent/skills")({
  component: AgentSkillsRoute,
})

function AgentSkillsRoute() {
  return <SkillsPageLazy />
}

const SkillsPageLazy = lazyRouteComponent(
  () => import("@/components/agent/skills/skills-page"),
  "SkillsPage",
)
