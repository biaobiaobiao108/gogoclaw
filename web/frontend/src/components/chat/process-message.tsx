import { IconBrain, IconChevronRight, IconTool } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { AssistantMessage } from "@/components/chat/assistant-message"
import type { ChatActivity, ChatMessage, ChatStoreState } from "@/store/chat"

export function ProcessMessage({
  messages,
  expanded,
  onToggle,
  streamingMessageId,
  activeTurn,
  activities = [],
}: {
  messages: ChatMessage[]
  expanded: boolean
  onToggle: () => void
  streamingMessageId?: string
  activeTurn?: ChatStoreState["activeTurn"]
  activities?: ChatActivity[]
}) {
  const { t } = useTranslation()
  const calls = messages.reduce(
    (total, message) => total + (message.toolCalls?.length ?? 0),
    0,
  )
  const isStreamingThought = Boolean(
    streamingMessageId &&
    messages.some((m) => m.id === streamingMessageId && m.kind === "thought"),
  )
  const turnActivities = activeTurn?.activities ?? []
  const isActive = Boolean(activeTurn || isStreamingThought)
  const currentActivity = isActive
    ? (activeTurn?.activity ??
      [...turnActivities, ...activities]
        .reverse()
        .find((activity) => activity.status === "running"))
    : undefined
  const label = currentActivity?.title
    ? currentActivity.title
    : isStreamingThought
      ? t("chat.reasoningStreamingLabel")
      : activeTurn?.phase === "tools"
        ? t("chat.workingWithTools")
        : activeTurn?.phase === "finalizing"
          ? t("chat.workingFinalizing")
          : t("chat.processLabel")
  return (
    <div className="text-muted-foreground text-sm">
      <button
        type="button"
        className="hover:bg-muted flex min-h-10 items-center gap-2 rounded-lg px-2 transition-colors"
        aria-expanded={expanded}
        onClick={onToggle}
      >
        {isActive ? (
          currentActivity ? (
            <IconTool className="text-accent size-3.5" />
          ) : (
            <IconBrain className="text-accent size-3.5" />
          )
        ) : null}
        <span className={isActive ? "process-shimmer" : undefined}>
          {label}
        </span>
        {calls > 0 && <span>{t("chat.toolCallCount", { count: calls })}</span>}
        <IconChevronRight
          className={`size-4 transition-transform ${expanded ? "rotate-90" : ""}`}
        />
      </button>
      {expanded && (
        <div className="border-border mt-2 space-y-3 border-l pl-4">
          {messages.map((message) => (
            <AssistantMessage
              key={message.id}
              {...message}
              detailOpen
              isStreaming={message.id === streamingMessageId}
            />
          ))}
        </div>
      )}
    </div>
  )
}
