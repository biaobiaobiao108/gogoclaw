import {
  createFileRoute,
  lazyRouteComponent,
  Outlet,
  useRouterState,
} from "@tanstack/react-router"

export const Route = createFileRoute("/config")({
  component: ConfigRouteLayout,
})

function ConfigRouteLayout() {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })

  if (pathname === "/config") {
    return <ConfigPageLazy />
  }

  return <Outlet />
}

const ConfigPageLazy = lazyRouteComponent(
  () => import("@/components/config/config-page"),
  "ConfigPage",
)
