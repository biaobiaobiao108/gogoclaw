import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router"

export const Route = createFileRoute("/channels/$name")({
  component: ChannelsByNameRoute,
})

function ChannelsByNameRoute() {
  const { name } = Route.useParams()

  return <ChannelConfigPageLazy key={name} channelName={name} />
}

const ChannelConfigPageLazy = lazyRouteComponent(
  () => import("@/components/channels/channel-config-page"),
  "ChannelConfigPage",
)
