import { useTranslation } from "react-i18next"

import { ActiveWorkIndicator } from "@/components/chat/active-work-indicator"
import { ActivityTimeline } from "@/components/chat/activity-timeline"
import { AssistantMessage } from "@/components/chat/assistant-message"
import type { ChatInputDisabledReason } from "@/components/chat/chat-composer"
import { ChatEmptyState } from "@/components/chat/chat-empty-state"
import type { ChatStreamRow } from "@/components/chat/chat-page-logic"
import { ProcessMessage } from "@/components/chat/process-message"
import { UserMessage } from "@/components/chat/user-message"
import { Button } from "@/components/ui/button"
import type {
  ChatBranchGroup,
  ChatFeedbackValue,
  ChatMessage,
  ChatStoreState,
  ConnectionState,
} from "@/store/chat"

interface ChatStreamRowContentProps {
  row: ChatStreamRow
  isLoadingOlderHistory?: boolean
  onLoadOlder: () => void
  onRetryHistory: () => void
  modelsLoading?: boolean
  modelsError: boolean
  hasDefaultModel: boolean
  hasAvailableModels: boolean
  defaultModelName: string
  inputDisabledReason: ChatInputDisabledReason | null
  onSelectPrompt: (prompt: string) => void
  branchGroup?: ChatBranchGroup
  onFeedback: (messageId: string, value: ChatFeedbackValue) => void
  onRegenerate: (messageId: string) => void
  onSelectBranch: (branchId: string) => void
  messageActionsDisabled: boolean
  isBranchSwitching?: boolean
  streamingMessageId?: string
  onEditMessage: (message: ChatMessage) => void
  connectionState: ConnectionState
  onRetryMessage: (messageId: string) => void
  activeTurn?: ChatStoreState["activeTurn"]
  recentActivities: ChatStoreState["recentActivities"]
  expandedProcesses: Record<string, boolean>
  onToggleProcess: (messageId: string) => void
}

export function ChatStreamRowContent({
  row,
  isLoadingOlderHistory,
  onLoadOlder,
  onRetryHistory,
  modelsLoading,
  modelsError,
  hasDefaultModel,
  hasAvailableModels,
  defaultModelName,
  inputDisabledReason,
  onSelectPrompt,
  branchGroup,
  onFeedback,
  onRegenerate,
  onSelectBranch,
  messageActionsDisabled,
  isBranchSwitching,
  streamingMessageId,
  onEditMessage,
  connectionState,
  onRetryMessage,
  activeTurn,
  recentActivities,
  expandedProcesses,
  onToggleProcess,
}: ChatStreamRowContentProps) {
  const { t } = useTranslation()

  if (row.kind === "loadOlder") {
    return (
      <div className="flex justify-center">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onLoadOlder}
          disabled={isLoadingOlderHistory}
        >
          {isLoadingOlderHistory
            ? t("chat.loadingEarlierMessages")
            : t("chat.loadEarlierMessages")}
        </Button>
      </div>
    )
  }

  if (row.kind === "historyLoading") {
    return (
      <div className="space-y-3" aria-busy="true">
        <span className="sr-only">{t("chat.loadingConversation")}</span>
        {[72, 88, 56].map((width) => (
          <div
            key={width}
            className="bg-muted/50 h-4 animate-pulse rounded motion-reduce:animate-none"
            style={{ width: `${width}%` }}
            aria-hidden="true"
          />
        ))}
      </div>
    )
  }

  if (row.kind === "historyError") {
    return (
      <div
        className="border-border bg-card flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3"
        role="alert"
      >
        <div>
          <p className="text-sm font-medium">{t("chat.historyLoadTitle")}</p>
          <p className="text-muted-foreground mt-0.5 text-xs">
            {t("chat.historyLoadFailed")}
          </p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={onRetryHistory}
        >
          {t("chat.historyRetry")}
        </Button>
      </div>
    )
  }

  if (row.kind === "empty") {
    if (modelsLoading && !hasDefaultModel) {
      return (
        <div role="status">{t("chat.disabledPlaceholder.modelsLoading")}</div>
      )
    }
    if (modelsError && !hasDefaultModel) return null
    return (
      <ChatEmptyState
        hasAvailableModels={hasAvailableModels}
        defaultModelName={defaultModelName}
        inputDisabledReason={inputDisabledReason}
        onSelectPrompt={onSelectPrompt}
      />
    )
  }

  if (row.kind === "message") {
    if (row.message.role === "assistant") {
      return (
        <AssistantMessage
          content={row.message.content}
          attachments={row.message.attachments}
          kind={row.message.kind}
          modelName={row.message.modelName}
          toolCalls={row.message.toolCalls}
          feedback={row.message.feedback}
          sources={row.message.sources}
          branchGroup={branchGroup}
          onFeedback={(value) => onFeedback(row.message.id, value)}
          onRegenerate={() => onRegenerate(row.message.id)}
          onSelectBranch={onSelectBranch}
          actionsDisabled={messageActionsDisabled}
          branchSwitchPending={isBranchSwitching}
          timestamp={row.message.timestamp}
          isStreaming={row.message.id === streamingMessageId}
        />
      )
    }
    return (
      <UserMessage
        content={row.message.content}
        attachments={row.message.attachments}
        timestamp={row.message.timestamp}
        deliveryStatus={row.message.deliveryStatus}
        onEdit={() => onEditMessage(row.message)}
        interactionsDisabled={messageActionsDisabled}
        onRetry={
          connectionState === "connected"
            ? () => onRetryMessage(row.message.id)
            : undefined
        }
      />
    )
  }

  if (row.kind === "process") {
    return (
      <ProcessMessage
        messages={row.messages}
        streamingMessageId={streamingMessageId}
        activeTurn={row.active ? activeTurn : undefined}
        activities={row.active ? (recentActivities ?? []) : []}
        expanded={expandedProcesses[row.messages[0].id] ?? false}
        onToggle={() => onToggleProcess(row.messages[0].id)}
      />
    )
  }

  if (row.kind === "working") {
    return (
      <ActiveWorkIndicator
        activeTurn={activeTurn}
        activities={recentActivities ?? []}
      />
    )
  }

  return (
    <ActivityTimeline
      plan={row.plan}
      activities={row.activities}
      history={row.history}
    />
  )
}
