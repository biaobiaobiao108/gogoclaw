import { IconActivity, IconTool } from "@tabler/icons-react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"

import type { ChatActivity, ChatStoreState } from "@/store/chat"

function activeLabel({
  t,
  activeTurn,
  activities,
  waitingForApproval,
  reconnecting,
  hasText,
}: {
  t: TFunction
  activeTurn?: ChatStoreState["activeTurn"]
  activities: ChatActivity[]
  waitingForApproval?: boolean
  reconnecting?: boolean
  hasText?: boolean
}) {
  if (reconnecting) return t("chat.reconnectingReply")
  if (waitingForApproval) return t("chat.waitingForApproval")
  const turnActivities = activeTurn?.activities ?? []
  const current =
    activeTurn?.activity ??
    [...turnActivities, ...activities]
      .reverse()
      .find((activity) => activity.status === "running")
  if (current?.title) return current.title
  if (activeTurn?.phase === "tools") return t("chat.workingWithTools")
  if (activeTurn?.phase === "finalizing") return t("chat.workingFinalizing")
  return t(hasText ? "chat.workingContinuing" : "chat.working")
}

export function ActiveWorkIndicator({
  activeTurn,
  activities = [],
  waitingForApproval,
  reconnecting,
  hasText,
}: {
  activeTurn?: ChatStoreState["activeTurn"]
  activities?: ChatActivity[]
  waitingForApproval?: boolean
  reconnecting?: boolean
  hasText?: boolean
}) {
  const { t } = useTranslation()
  const label = activeLabel({
    t,
    activeTurn,
    activities,
    waitingForApproval,
    reconnecting,
    hasText,
  })
  const turnActivities = activeTurn?.activities ?? []
  const isTool = Boolean(
    activeTurn?.activity ??
    [...turnActivities, ...activities].find(
      (activity) => activity.status === "running",
    ) ??
    activeTurn?.phase === "tools",
  )

  return (
    <div
      className="text-muted-foreground flex min-h-10 items-center gap-2 px-2 text-sm"
      role="status"
    >
      {isTool ? (
        <IconTool className="text-accent size-4 shrink-0" />
      ) : (
        <IconActivity className="text-accent size-4 shrink-0" />
      )}
      <span className="process-shimmer min-w-0 truncate">{label}</span>
    </div>
  )
}
