import { useTranslation } from "react-i18next"

import { ActiveWorkIndicator } from "@/components/chat/active-work-indicator"
import { ActivityTimeline } from "@/components/chat/activity-timeline"
import {
  AssistantMessage,
  AssistantMessageActions,
} from "@/components/chat/assistant-message"
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
  waitingForApproval?: boolean
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
  waitingForApproval,
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

  if (row.kind === "reply") {
    const { lastText } = row
    const lastProcess = [...row.parts]
      .reverse()
      .find((part) => part.kind === "process")
    const reconnecting = connectionState !== "connected" && row.active
    const copyText =
      lastText && !row.active
        ? row.parts
            .flatMap((part) =>
              part.kind === "text" && part.message.content.trim()
                ? [part.message.content]
                : [],
            )
            .join("\n\n")
        : ""
    return (
      <article className="group w-full">
        {row.parts.map((part, index) => {
          if (part.kind === "steering") {
            return (
              <div
                key={part.message.id}
                className="text-muted-foreground py-2 text-sm"
              >
                <span className="text-xs">{t("chat.steeringInstruction")}</span>
                <UserMessage
                  content={part.message.content}
                  attachments={part.message.attachments}
                  deliveryStatus={part.message.deliveryStatus}
                  onRetry={
                    connectionState === "connected"
                      ? () => onRetryMessage(part.message.id)
                      : undefined
                  }
                />
              </div>
            )
          }
          if (part.kind === "text") {
            return (
              <AssistantMessage
                key={part.message.id}
                content={part.message.content}
                attachments={part.message.attachments}
                sources={part.message.sources}
                isStreaming={part.message.id === streamingMessageId}
              />
            )
          }
          const active = row.active && index === row.parts.length - 1
          const processId = part.messages[0].id
          return (
            <ProcessMessage
              key={processId}
              messages={part.messages}
              streamingMessageId={streamingMessageId}
              isTurnActive={active}
              activeTurn={active ? activeTurn : undefined}
              activities={active ? recentActivities : undefined}
              execution={part === lastProcess ? row.execution : undefined}
              waitingForApproval={active && waitingForApproval}
              reconnecting={active && reconnecting}
              expanded={expandedProcesses[processId] ?? false}
              onToggle={() => onToggleProcess(processId)}
            />
          )
        })}
        {row.active && row.parts.at(-1)?.kind !== "process" && (
          <ActiveWorkIndicator
            activeTurn={activeTurn}
            activities={recentActivities}
            waitingForApproval={waitingForApproval}
            reconnecting={reconnecting}
            hasText={Boolean(lastText)}
          />
        )}
        {row.execution && !lastProcess && (
          <ActivityTimeline
            plan={row.execution.plan}
            activities={row.execution.activities}
          />
        )}
        {lastText && copyText && (
          <AssistantMessageActions
            content={copyText}
            modelName={lastText.modelName}
            timestamp={lastText.timestamp}
            feedback={lastText.feedback}
            branchGroup={branchGroup}
            onFeedback={(value) => onFeedback(lastText.id, value)}
            onRegenerate={() => onRegenerate(lastText.id)}
            onSelectBranch={onSelectBranch}
            actionsDisabled={messageActionsDisabled}
            branchSwitchPending={isBranchSwitching}
          />
        )}
      </article>
    )
  }

  if (row.kind === "working") {
    return (
      <ActiveWorkIndicator
        activeTurn={activeTurn}
        activities={recentActivities ?? []}
        waitingForApproval={waitingForApproval}
        reconnecting={connectionState !== "connected"}
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
