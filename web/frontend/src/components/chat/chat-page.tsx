import { IconArrowDown, IconLayoutSidebar, IconPlus } from "@tabler/icons-react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useAtom } from "jotai"
import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"

import { ActivityTimeline } from "@/components/chat/activity-timeline"
import { ApprovalPanel } from "@/components/chat/approval-panel"
import { AssistantMessage } from "@/components/chat/assistant-message"
import {
  ChatComposer,
  type ChatInputDisabledReason,
} from "@/components/chat/chat-composer"
import { ChatEmptyState } from "@/components/chat/chat-empty-state"
import { ModelSelector } from "@/components/chat/model-selector"
import { ProcessMessage } from "@/components/chat/process-message"
import { SessionHistoryMenu } from "@/components/chat/session-history-menu"
import { TypingIndicator } from "@/components/chat/typing-indicator"
import { UserMessage } from "@/components/chat/user-message"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { retryChatMessage } from "@/features/chat/controller"
import { groupChatMessages } from "@/features/chat/group-messages"
import {
  buildChatImageAttachments,
  CHAT_IMAGE_ACCEPT,
  getTransferredFiles,
  hasFileTransfer,
} from "@/features/chat/image-input"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import { usePicoChat } from "@/hooks/use-pico-chat"
import { useSessionHistory } from "@/hooks/use-session-history"
import { chatDraftsAtom, getChatState } from "@/store/chat"
import type {
  ChatActivity,
  ChatAttachment,
  ChatMessage,
  ChatPlanStep,
  ChatTurnHistory,
  ConnectionState,
} from "@/store/chat"
import type { GatewayState } from "@/store/gateway"

function resolveChatInputDisabledReason({
  hasDefaultModel,
  connectionState,
  gatewayState,
}: {
  hasDefaultModel: boolean
  connectionState: ConnectionState
  gatewayState: GatewayState
}): ChatInputDisabledReason | null {
  if (gatewayState === "unknown") {
    return "gatewayUnknown"
  }
  if (gatewayState === "starting") {
    return "gatewayStarting"
  }
  if (gatewayState === "restarting") {
    return "gatewayRestarting"
  }
  if (gatewayState === "stopping") {
    return "gatewayStopping"
  }
  if (gatewayState === "stopped") {
    return "gatewayStopped"
  }
  if (gatewayState === "error") {
    return "gatewayError"
  }
  if (connectionState === "connecting") {
    return "websocketConnecting"
  }
  if (connectionState === "error") {
    return "websocketError"
  }
  if (connectionState === "disconnected") {
    return "websocketDisconnected"
  }
  if (!hasDefaultModel) {
    return "noDefaultModel"
  }
  return null
}

type ChatStreamRow =
  | { kind: "loadOlder" }
  | { kind: "historyLoading" }
  | { kind: "historyError" }
  | { kind: "empty" }
  | { kind: "message"; message: ChatMessage }
  | { kind: "process"; messages: ChatMessage[] }
  | { kind: "typing" }
  | {
      kind: "activity"
      plan?: ChatPlanStep[]
      activities?: ChatActivity[]
      history?: ChatTurnHistory[]
    }

function chatStreamRowKey(row: ChatStreamRow): string {
  return row.kind === "message"
    ? `message:${row.message.id}`
    : row.kind === "process"
      ? `process:${row.messages[0].id}`
      : row.kind
}

function estimateChatStreamRow(row: ChatStreamRow): number {
  switch (row.kind) {
    case "loadOlder":
      return 56
    case "historyLoading":
    case "historyError":
      return 104
    case "empty":
      return 420
    case "typing":
      return 56
    case "activity":
      return 180
    default:
      return 112
  }
}

export function ChatPage() {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyCollapsed, setHistoryCollapsed] = useState(false)
  const [expandedProcesses, setExpandedProcesses] = useState<
    Record<string, boolean>
  >({})
  const [isAtBottom, setIsAtBottom] = useState(true)
  const [hasScrolled, setHasScrolled] = useState(false)
  const [drafts, setDrafts] = useAtom(chatDraftsAtom)
  const [isDragActive, setIsDragActive] = useState(false)

  const {
    messages,
    connectionState,
    isTyping,
    activeTurn,
    recentActivities,
    recentPlan,
    turnHistory,
    pendingApproval,
    interrupted,
    activeSessionId,
    contextUsage,
    sendMessage,
    respondToApproval,
    switchSession,
    newChat,
    isHistoryLoading,
    historyError,
    isLoadingOlderHistory,
    hasOlderHistory,
    loadOlderHistory,
    retryHistory,
  } = usePicoChat()

  const { input = "", attachments = [] } = drafts[activeSessionId] ?? {}
  const updateDraft = (
    sessionId: string,
    update: (draft: { input: string; attachments: ChatAttachment[] }) => {
      input: string
      attachments: ChatAttachment[]
    },
  ) => {
    setDrafts((previous) => {
      const next = { ...previous }
      const draft = update(
        previous[sessionId] ?? { input: "", attachments: [] },
      )
      if (!draft.input && draft.attachments.length === 0) delete next[sessionId]
      else next[sessionId] = draft
      return next
    })
  }
  const setInput = (input: string) =>
    updateDraft(activeSessionId, (draft) => ({ ...draft, input }))
  const setAttachments = (
    update:
      ChatAttachment[] | ((previous: ChatAttachment[]) => ChatAttachment[]),
  ) =>
    updateDraft(activeSessionId, (draft) => ({
      ...draft,
      attachments:
        typeof update === "function" ? update(draft.attachments) : update,
    }))

  const { state: gwState } = useGateway()
  const isGatewayRunning = gwState === "running"

  const {
    defaultModelName,
    hasAvailableModels,
    apiKeyModels,
    oauthModels,
    localModels,
    settingDefault,
    handleSetDefault,
    modelsLoading,
    modelsError,
    reloadModels,
  } = useChatModels({ isConnected: isGatewayRunning })
  const hasDefaultModel = Boolean(defaultModelName)
  const inputDisabledReason = resolveChatInputDisabledReason({
    hasDefaultModel,
    connectionState,
    gatewayState: gwState,
  })
  const composerDisabledReason =
    !hasDefaultModel && modelsLoading
      ? "modelsLoading"
      : !hasDefaultModel && modelsError
        ? "modelsError"
        : inputDisabledReason
  const canInput = composerDisabledReason === null

  const {
    sessions,
    hasMore,
    loadError,
    loadErrorMessage,
    isLoading: isLoadingHistory,
    deleteError,
    loadSessions,
    handleDeleteSession,
  } = useSessionHistory({
    activeSessionId,
    onDeletedActiveSession: newChat,
  })

  useEffect(() => {
    void loadSessions(true)
    // Refresh titles after the current response finishes or the session changes.
  }, [activeSessionId, Boolean(activeTurn), isTyping])

  const syncScrollState = (element: HTMLDivElement) => {
    const { clientHeight, scrollHeight, scrollTop } = element
    setHasScrolled(scrollTop > 0)
    setIsAtBottom(scrollHeight - scrollTop <= clientHeight + 10)
  }

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    syncScrollState(e.currentTarget)
  }

  const handleLoadOlderHistory = async () => {
    const sessionId = activeSessionId
    const element = scrollRef.current
    const preserveFromBottom = element
      ? element.scrollHeight - element.scrollTop
      : 0
    const loaded = await loadOlderHistory()
    if (!loaded || !element || getChatState().activeSessionId !== sessionId) {
      return
    }
    requestAnimationFrame(() => {
      element.scrollTop = element.scrollHeight - preserveFromBottom
    })
  }

  useEffect(() => {
    const preventFileNavigation = (event: globalThis.DragEvent) => {
      if (Array.from(event.dataTransfer?.types ?? []).includes("Files")) {
        event.preventDefault()
      }
    }
    window.addEventListener("dragover", preventFileNavigation)
    window.addEventListener("drop", preventFileNavigation)
    return () => {
      window.removeEventListener("dragover", preventFileNavigation)
      window.removeEventListener("drop", preventFileNavigation)
    }
  }, [])

  const handleSend = () => {
    if ((!input.trim() && attachments.length === 0) || !canInput) return
    if (
      sendMessage({
        content: input,
        attachments,
      })
    ) {
      setInput("")
      setAttachments([])
    }
  }

  const handleAddImages = () => {
    if (!canInput) return
    fileInputRef.current?.click()
  }

  const handleRemoveAttachment = (index: number) => {
    setAttachments((prev) => prev.filter((_, itemIndex) => itemIndex !== index))
  }

  const appendImageFiles = async (files: readonly File[]) => {
    if (!canInput || files.length === 0) {
      return
    }

    const nextAttachments = await buildChatImageAttachments(files, t)
    if (nextAttachments.length === 0) {
      return
    }

    updateDraft(activeSessionId, (draft) => ({
      ...draft,
      attachments: [...draft.attachments, ...nextAttachments],
    }))
  }

  const handleImageSelection = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ""

    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const resetDragState = () => {
    dragDepthRef.current = 0
    setIsDragActive(false)
  }

  const handleComposerPaste = async (
    event: ClipboardEvent<HTMLTextAreaElement>,
  ) => {
    const files = getTransferredFiles(event.clipboardData)
    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const handleComposerDragEnter = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      return
    }
    dragDepthRef.current += 1
    setIsDragActive(true)
  }

  const handleComposerDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      resetDragState()
      return
    }
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
    if (dragDepthRef.current === 0) {
      setIsDragActive(false)
    }
  }

  const handleComposerDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    event.dataTransfer.dropEffect = canInput ? "copy" : "none"
  }

  const handleComposerDrop = async (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    const files = getTransferredFiles(event.dataTransfer)
    resetDragState()

    if (!canInput || files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const canSubmit =
    canInput && (Boolean(input.trim()) || attachments.length > 0)

  const streamRows = useMemo<ChatStreamRow[]>(() => {
    const rows: ChatStreamRow[] = []
    if (hasOlderHistory && !isHistoryLoading) rows.push({ kind: "loadOlder" })
    if (isHistoryLoading) rows.push({ kind: "historyLoading" })
    if (!isHistoryLoading && historyError) rows.push({ kind: "historyError" })
    if (
      !isHistoryLoading &&
      !historyError &&
      messages.length === 0 &&
      !isTyping
    ) {
      rows.push({ kind: "empty" })
    }
    rows.push(...groupChatMessages(messages))
    if (isTyping) rows.push({ kind: "typing" })
    if (!isHistoryLoading && !historyError && !activeTurn) {
      rows.push({
        kind: "activity",
        plan: recentPlan,
        activities: recentActivities,
        history: turnHistory,
      })
    }
    return rows
  }, [
    activeTurn,
    hasOlderHistory,
    historyError,
    isHistoryLoading,
    isTyping,
    messages,
    recentActivities,
    recentPlan,
    turnHistory,
  ])

  const streamVirtualizer = useVirtualizer({
    count: streamRows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (index) => {
      const row = streamRows[index]
      return row ? estimateChatStreamRow(row) : 112
    },
    getItemKey: (index) => {
      const row = streamRows[index]
      return row ? chatStreamRowKey(row) : index
    },
    overscan: 6,
  })

  const streamingMessageId = (() => {
    if (!activeTurn && !isTyping) {
      return undefined
    }
    for (let i = messages.length - 1; i >= 0; i -= 1) {
      if (messages[i].role === "assistant") {
        return messages[i].id
      }
    }
    return undefined
  })()

  useEffect(() => {
    if (!scrollRef.current) return
    if (isAtBottom) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
    syncScrollState(scrollRef.current)
  }, [streamRows, isAtBottom])

  return (
    <div className="chat-surface flex h-full min-w-0">
      <SessionHistoryMenu
        sessions={sessions}
        activeSessionId={activeSessionId}
        hasMore={hasMore}
        loadError={loadError}
        loadErrorMessage={loadErrorMessage}
        isLoading={isLoadingHistory}
        deleteError={deleteError}
        collapsed={historyCollapsed}
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        onRefresh={() => void loadSessions(true)}
        onLoadMore={() => void loadSessions(false)}
        onSwitchSession={switchSession}
        onDeleteSession={handleDeleteSession}
      />
      <div className="relative flex min-w-0 flex-1 flex-col">
        <PageHeader
          title={t("navigation.chat")}
          className={`transition-shadow ${
            hasScrolled ? "shadow-xs" : "shadow-none"
          }`}
          titleExtra={
            hasAvailableModels && (
              <ModelSelector
                defaultModelName={defaultModelName}
                apiKeyModels={apiKeyModels}
                oauthModels={oauthModels}
                localModels={localModels}
                disabled={settingDefault}
                onValueChange={handleSetDefault}
              />
            )
          }
        >
          <Button
            variant="ghost"
            size="sm"
            onClick={newChat}
            className="h-8 gap-2 px-3 text-sm font-medium"
            aria-label={t("chat.newChat")}
          >
            <IconPlus className="size-3.5" />
            <span className="hidden sm:inline">{t("chat.newChat")}</span>
          </Button>

          <Button
            variant="ghost"
            size="sm"
            className="h-8 gap-2 text-sm"
            onClick={() => {
              if (window.matchMedia("(min-width: 768px)").matches)
                setHistoryCollapsed((value) => !value)
              else setHistoryOpen(true)
            }}
            aria-label={t("chat.history")}
          >
            <IconLayoutSidebar className="size-4" />
          </Button>
        </PageHeader>
        {modelsError && (
          <div
            role="alert"
            className="mx-4 flex shrink-0 items-center justify-between gap-3 py-2 text-sm"
          >
            <span>{t("chat.modelsLoadFailed")}</span>
            <Button
              variant="outline"
              size="sm"
              disabled={modelsLoading}
              onClick={() => void reloadModels()}
            >
              {t("chat.historyRetry")}
            </Button>
          </div>
        )}

        {/* Main chat stream container */}
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="min-h-0 flex-1 overflow-y-auto px-4 py-6 md:px-8"
        >
          <div
            className="relative mx-auto w-full max-w-3xl"
            style={{ height: `${streamVirtualizer.getTotalSize()}px` }}
            role="list"
            aria-label={t("chat.conversation")}
          >
            {streamVirtualizer.getVirtualItems().map((virtualRow) => {
              const row = streamRows[virtualRow.index]
              if (!row) return null
              return (
                <div
                  key={virtualRow.key}
                  ref={streamVirtualizer.measureElement}
                  data-index={virtualRow.index}
                  role="listitem"
                  className="absolute top-0 left-0 w-full pb-6"
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {row.kind === "loadOlder" && (
                    <div className="flex justify-center">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void handleLoadOlderHistory()}
                        disabled={isLoadingOlderHistory}
                      >
                        {isLoadingOlderHistory
                          ? t("chat.loadingEarlierMessages")
                          : t("chat.loadEarlierMessages")}
                      </Button>
                    </div>
                  )}
                  {row.kind === "historyLoading" && (
                    <div className="space-y-3" aria-busy="true">
                      <span className="sr-only">
                        {t("chat.loadingConversation")}
                      </span>
                      {[72, 88, 56].map((width) => (
                        <div
                          key={width}
                          className="bg-muted/50 h-4 animate-pulse rounded motion-reduce:animate-none"
                          style={{ width: `${width}%` }}
                          aria-hidden="true"
                        />
                      ))}
                    </div>
                  )}
                  {row.kind === "historyError" && (
                    <div
                      className="border-border bg-card flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3"
                      role="alert"
                    >
                      <div>
                        <p className="text-sm font-medium">
                          {t("chat.historyLoadTitle")}
                        </p>
                        <p className="text-muted-foreground mt-0.5 text-xs">
                          {t("chat.historyLoadFailed")}
                        </p>
                      </div>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => void retryHistory()}
                      >
                        {t("chat.historyRetry")}
                      </Button>
                    </div>
                  )}
                  {row.kind === "empty" &&
                    (modelsLoading && !hasDefaultModel ? (
                      <div role="status">
                        {t("chat.disabledPlaceholder.modelsLoading")}
                      </div>
                    ) : modelsError && !hasDefaultModel ? null : (
                      <ChatEmptyState
                        hasAvailableModels={hasAvailableModels}
                        defaultModelName={defaultModelName}
                        isConnected={isGatewayRunning}
                      />
                    ))}
                  {row.kind === "message" &&
                    (row.message.role === "assistant" ? (
                      <AssistantMessage
                        content={row.message.content}
                        attachments={row.message.attachments}
                        kind={row.message.kind}
                        modelName={row.message.modelName}
                        toolCalls={row.message.toolCalls}
                        timestamp={row.message.timestamp}
                        isStreaming={row.message.id === streamingMessageId}
                      />
                    ) : (
                      <UserMessage
                        content={row.message.content}
                        attachments={row.message.attachments}
                        timestamp={row.message.timestamp}
                        deliveryStatus={row.message.deliveryStatus}
                        onRetry={
                          connectionState === "connected"
                            ? () => retryChatMessage(row.message.id)
                            : undefined
                        }
                      />
                    ))}
                  {row.kind === "process" && (
                    <ProcessMessage
                      messages={row.messages}
                      expanded={expandedProcesses[row.messages[0].id] ?? false}
                      onToggle={() =>
                        setExpandedProcesses((previous) => ({
                          ...previous,
                          [row.messages[0].id]: !previous[row.messages[0].id],
                        }))
                      }
                    />
                  )}
                  {row.kind === "typing" && <TypingIndicator />}
                  {row.kind === "activity" && (
                    <ActivityTimeline
                      plan={row.plan}
                      activities={row.activities}
                      history={row.history}
                    />
                  )}
                </div>
              )
            })}
          </div>
        </div>

        {!isAtBottom && (
          <Button
            variant="outline"
            size="icon"
            className="absolute right-6 bottom-40 z-30 rounded-full"
            aria-label={t("chat.scrollToBottom")}
            onClick={() => {
              const element = scrollRef.current
              if (element) element.scrollTop = element.scrollHeight
              setIsAtBottom(true)
            }}
          >
            <IconArrowDown className="size-4" />
          </Button>
        )}
        <input
          ref={fileInputRef}
          type="file"
          accept={CHAT_IMAGE_ACCEPT}
          multiple
          className="hidden"
          onChange={handleImageSelection}
        />

        {activeTurn && (
          <div
            role="status"
            className="text-muted-foreground mx-auto w-full max-w-3xl px-4 pb-1 text-xs"
          >
            {t("chat.taskRunning", { iteration: activeTurn.iteration })}
          </div>
        )}
        {activeTurn?.plan && activeTurn.plan.length > 0 && (
          <details
            open
            className="mx-auto max-h-[25dvh] w-full max-w-3xl shrink-0 overflow-y-auto px-4 pb-2 text-xs"
          >
            <summary className="text-muted-foreground cursor-pointer">
              {t("chat.taskPlan")}
            </summary>
            <ol className="mt-2 space-y-1 pl-4">
              {activeTurn.plan.map((step, index) => (
                <li key={index} className="text-muted-foreground">
                  <span aria-label={t(`chat.planStatus.${step.status}`)}>
                    {step.status === "completed"
                      ? "✓"
                      : step.status === "in_progress"
                        ? "●"
                        : "○"}
                  </span>{" "}
                  {step.text}
                </li>
              ))}
            </ol>
          </details>
        )}
        {interrupted && !activeTurn && !isTyping && canInput && (
          <div className="mx-auto flex w-full max-w-3xl items-center justify-between gap-3 px-4 pb-2 text-xs">
            <span className="text-muted-foreground">
              {t("chat.taskInterrupted")}
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => sendMessage({ content: t("chat.continuePrompt") })}
            >
              {t("chat.continueTask")}
            </Button>
          </div>
        )}
        {pendingApproval && (
          <ApprovalPanel
            approval={pendingApproval}
            onRespond={respondToApproval}
          />
        )}
        <ChatComposer
          input={input}
          attachments={attachments}
          onInputChange={setInput}
          onAddImages={handleAddImages}
          onPaste={handleComposerPaste}
          onDragEnter={handleComposerDragEnter}
          onDragLeave={handleComposerDragLeave}
          onDragOver={handleComposerDragOver}
          onDrop={handleComposerDrop}
          onRemoveAttachment={handleRemoveAttachment}
          onSend={handleSend}
          onStop={() => sendMessage({ content: "/stop" })}
          isRunning={Boolean(activeTurn) || isTyping}
          onContextDetail={() => {
            sendMessage({ content: "/context", attachments: [] })
          }}
          inputDisabledReason={composerDisabledReason}
          canSend={canSubmit}
          isDragActive={isDragActive}
          contextUsage={contextUsage}
        />
      </div>
    </div>
  )
}
