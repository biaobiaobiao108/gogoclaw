import { IconChevronRight } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { AssistantMessage } from "@/components/chat/assistant-message"
import type { ChatMessage } from "@/store/chat"

export function ProcessMessage({
  messages,
  expanded,
  onToggle,
}: {
  messages: ChatMessage[]
  expanded: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const calls = messages.reduce(
    (total, message) => total + (message.toolCalls?.length ?? 0),
    0,
  )
  return (
    <div className="text-muted-foreground text-sm">
      <button
        type="button"
        className="hover:bg-muted flex min-h-10 items-center gap-2 rounded-lg px-2 transition-colors"
        aria-expanded={expanded}
        onClick={onToggle}
      >
        <span>{t("chat.processLabel")}</span>
        {calls > 0 && <span>{t("chat.toolCallCount", { count: calls })}</span>}
        <IconChevronRight
          className={`size-4 transition-transform ${expanded ? "rotate-90" : ""}`}
        />
      </button>
      {expanded && (
        <div className="border-border mt-2 space-y-3 border-l pl-4">
          {messages.map((message) => (
            <AssistantMessage key={message.id} {...message} detailOpen />
          ))}
        </div>
      )}
    </div>
  )
}
