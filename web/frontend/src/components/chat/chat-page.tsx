import { IconLayoutSidebar, IconLoader2, IconPlus } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { useAtom } from "jotai"
import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"

import { deleteSessionAttachment } from "@/api/sessions"
import { ChatComposer } from "@/components/chat/chat-composer"
import {
  ChatScrollToBottom,
  ChatTurnControls,
} from "@/components/chat/chat-page-notices"
import { ChatStreamRowContent } from "@/components/chat/chat-stream-row"
import { getCachedUploadLimit } from "@/components/chat/chat-upload-limit"
import { ModelSelector } from "@/components/chat/model-selector"
import { SessionHistoryMenu } from "@/components/chat/session-history-menu"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  retryChatMessage,
  saveMessageFeedback,
  selectChatBranch,
} from "@/features/chat/controller"
import { groupChatMessages } from "@/features/chat/group-messages"
import {
  CHAT_ATTACHMENT_ACCEPT,
  getTransferredFiles,
  hasFileTransfer,
  releaseAttachmentPreview,
  retryChatAttachment,
  uploadChatAttachments,
} from "@/features/chat/image-input"
import { useChatStreamViewport } from "@/features/chat/use-chat-stream-viewport"
import { saveSessionViewport } from "@/features/chat/viewport"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import { useIsHistorySheet } from "@/hooks/use-mobile"
import { usePicoChat } from "@/hooks/use-pico-chat"
import { isFirstUse, setFirstUse } from "@/lib/first-use"
import { type ChatDraft, chatDraftsAtom } from "@/store/chat"
import type { ChatAttachment, ChatMessage } from "@/store/chat"

import {
  type ChatStreamRow,
  resolveChatInputDisabledReason,
} from "./chat-page-logic"

export function ChatPage() {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)
  const isHistorySheet = useIsHistorySheet()

  const {
    messages,
    connectionState,
    isTyping,
    activeTurn,
    recentActivities,
    recentPlan,
    turnHistory,
    historyTurn,
    pendingApproval,
    interrupted,
    activeSessionId,
    pendingSessionId,
    contextUsage,
    branches,
    isBranchSwitching,
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
  const [firstUse, setFirstUseVisible] = useState(isFirstUse)
  const firstReplyReady =
    firstUse &&
    !activeTurn &&
    !isTyping &&
    !interrupted &&
    historyTurn?.status === "completed" &&
    messages.some(
      (message) =>
        message.role === "assistant" &&
        message.turnId === (historyTurn.replyTurnId ?? historyTurn.turnId) &&
        (!message.kind || message.kind === "normal") &&
        Boolean(message.content.trim()),
    )
  const dismissFirstUse = () => {
    setFirstUse(false)
    setFirstUseVisible(false)
  }

  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyCollapsed, setHistoryCollapsed] = useState(() => {
    try {
      return localStorage.getItem("gogoclaw:chat-history-collapsed") === "true"
    } catch {
      return false
    }
  })
  const [drafts, setDrafts] = useAtom(chatDraftsAtom)
  const [isDragActive, setIsDragActive] = useState(false)
  const [maxAttachmentSize, setMaxAttachmentSize] = useState(
    () => 20 * 1024 * 1024,
  )
  const composerTextareaRef = useRef<HTMLTextAreaElement>(null)

  const toggleHistoryCollapsed = useCallback(() => {
    setHistoryCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem("gogoclaw:chat-history-collapsed", String(next))
      } catch {}
      return next
    })
  }, [])

  const messageActionsDisabled = Boolean(
    activeTurn || isTyping || isBranchSwitching,
  )

  useEffect(() => {
    if (!isHistorySheet) setHistoryOpen(false)
  }, [isHistorySheet])

  const currentDraft = drafts[activeSessionId]
  const input = currentDraft?.input ?? ""
  const attachments = currentDraft?.attachments ?? []
  const thinkingLevel = currentDraft?.thinkingLevel ?? ""
  const editingMessageId = currentDraft?.editingMessageId ?? null

  const updateDraft = (
    sessionId: string,
    update: (draft: ChatDraft) => ChatDraft,
  ) => {
    setDrafts((previous) => {
      const next = { ...previous }
      const current = previous[sessionId] ?? { input: "", attachments: [] }
      const draft = update(current)
      if (
        !draft.input &&
        draft.attachments.length === 0 &&
        !draft.thinkingLevel &&
        !draft.editingMessageId
      ) {
        delete next[sessionId]
      } else {
        next[sessionId] = draft
      }
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
  const setThinkingLevel = (thinkingLevel: string) =>
    updateDraft(activeSessionId, (draft) => ({ ...draft, thinkingLevel }))
  const setEditingMessageId = (editingMessageId: string | null) =>
    updateDraft(activeSessionId, (draft) => ({ ...draft, editingMessageId }))

  useEffect(() => {
    let cancelled = false
    void getCachedUploadLimit().then((limit) => {
      if (!cancelled) setMaxAttachmentSize(limit)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const { state: gwState } = useGateway()
  const isGatewayRunning = gwState === "running"

  const {
    defaultModelName,
    hasAvailableModels,
    apiKeyModels,
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

  const handleNewChat = useCallback(() => {
    void newChat()
    requestAnimationFrame(() => {
      composerTextareaRef.current?.focus()
    })
  }, [newChat])

  const handleNewChatInProject = useCallback(
    (projectId: string) => {
      void newChat(projectId)
      setHistoryOpen(false)
      requestAnimationFrame(() => {
        composerTextareaRef.current?.focus()
      })
    },
    [newChat],
  )

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
    const readyAttachments = attachments.filter(
      (attachment) =>
        attachment.status === undefined || attachment.status === "ready",
    )
    if (
      (!input.trim() && readyAttachments.length === 0) ||
      !canInput ||
      attachments.some(
        (attachment) =>
          attachment.status === "uploading" || attachment.status === "failed",
      )
    )
      return
    if (
      sendMessage({
        content: input,
        attachments: readyAttachments,
        thinkingLevel,
        ...(editingMessageId
          ? { action: "edit" as const, targetMessageId: editingMessageId }
          : {}),
      })
    ) {
      setInput("")
      setAttachments([])
      setEditingMessageId(null)
      // Always auto-scroll on new user message send
      autoScrollEnabledRef.current = true
      requestAnimationFrame(() => {
        scrollToBottom("smooth")
      })
    }
  }

  const handleEditMessage = (message: ChatMessage) => {
    if (
      message.role !== "user" ||
      message.deliveryStatus === "sending" ||
      messageActionsDisabled
    )
      return
    setEditingMessageId(message.id)
    setInput(message.content)
    setAttachments(
      (message.attachments ?? []).map((attachment) => ({
        ...attachment,
        status: attachment.url ? ("ready" as const) : ("failed" as const),
      })),
    )
  }

  const handleRegenerateMessage = (messageId: string) => {
    if (!canInput || messageActionsDisabled) return
    sendMessage({
      content: "",
      action: "regenerate",
      targetMessageId: messageId,
      thinkingLevel,
    })
  }

  const branchByVersion = useMemo(() => {
    const map = new Map<string, NonNullable<typeof branches>[number]>()
    for (const group of branches ?? []) {
      for (const version of group.versions) map.set(version.id, group)
    }
    return map
  }, [branches])

  const cancelEditingMessage = () => {
    for (const attachment of attachments) {
      releaseAttachmentPreview(attachment)
      if (attachment.id && !attachment.persisted) {
        void deleteSessionAttachment(activeSessionId, attachment.id).catch(
          (error) => {
            console.warn("Failed to delete discarded attachment:", error)
          },
        )
      }
    }
    setEditingMessageId(null)
    setInput("")
    setAttachments([])
  }

  const handleAddAttachments = () => {
    if (!canInput) return
    fileInputRef.current?.click()
  }

  const handleRemoveAttachment = (index: number) => {
    const attachment = attachments[index]
    if (attachment) {
      releaseAttachmentPreview(attachment)
      if (attachment.id && !attachment.persisted) {
        void deleteSessionAttachment(activeSessionId, attachment.id).catch(
          (error) => {
            console.warn("Failed to delete attachment:", error)
          },
        )
      }
    }
    setAttachments((prev) => prev.filter((_, itemIndex) => itemIndex !== index))
  }

  const handleRetryAttachment = (index: number) => {
    const attachment = attachments[index]
    if (!attachment) return
    void retryChatAttachment({
      attachment,
      sessionId: activeSessionId,
      maxAttachmentSize,
      t,
      onAttachment: (next) => {
        setAttachments((previous) =>
          previous.map((item) =>
            item.clientId === next.clientId ? next : item,
          ),
        )
      },
    })
  }

  const appendFiles = async (files: readonly File[]) => {
    if (!canInput || files.length === 0) {
      return
    }

    const sessionId = activeSessionId
    await uploadChatAttachments({
      files,
      sessionId,
      maxAttachmentSize,
      t,
      onAttachment: (attachment) => {
        setAttachments((previous) => {
          const index = previous.findIndex(
            (item) => item.clientId && item.clientId === attachment.clientId,
          )
          if (index < 0) {
            if (attachment.status === "uploading")
              return [...previous, attachment]
            if (attachment.id && !attachment.persisted) {
              void deleteSessionAttachment(sessionId, attachment.id).catch(
                (error) => {
                  console.warn("Failed to delete discarded attachment:", error)
                },
              )
            }
            return previous
          }
          const next = [...previous]
          next[index] = attachment
          return next
        })
      },
    })
  }

  const handleImageSelection = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ""

    if (files.length === 0) {
      return
    }

    await appendFiles(files)
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

    await appendFiles(files)
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

    await appendFiles(files)
  }

  const canSubmit =
    canInput &&
    !attachments.some(
      (attachment) =>
        attachment.status === "uploading" || attachment.status === "failed",
    ) &&
    (Boolean(input.trim()) || attachments.length > 0)

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
    const executionByTurn = new Map(
      (turnHistory ?? []).map((turn) => [turn.turnId, turn]),
    )
    const groupedRows: ChatStreamRow[] = groupChatMessages(messages).map(
      (row) => {
        if (row.kind !== "reply") return row
        const execution = executionByTurn.get(row.id)
        if (!execution) return row
        executionByTurn.delete(row.id)
        return { ...row, execution }
      },
    )
    const lastGroupedIndex = activeTurn?.turnId
      ? groupedRows.findIndex(
          (row) => row.kind === "reply" && row.id === activeTurn.turnId,
        )
      : groupedRows.length - 1
    const lastGroupedRow = groupedRows[lastGroupedIndex]
    const replyIsActive =
      Boolean(activeTurn || isTyping) && lastGroupedRow?.kind === "reply"
    if (replyIsActive) {
      groupedRows[lastGroupedIndex] = { ...lastGroupedRow, active: true }
    }
    rows.push(...groupedRows)
    if ((isTyping || activeTurn) && !replyIsActive)
      rows.push({ kind: "working" })
    if (!isHistoryLoading && !historyError && !activeTurn) {
      rows.push({
        kind: "activity",
        plan: turnHistory?.length ? undefined : recentPlan,
        activities: turnHistory?.length ? undefined : recentActivities,
        history: [...executionByTurn.values()],
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

  const {
    scrollRef,
    streamContentRef,
    autoScrollEnabledRef,
    expandedProcesses,
    setExpandedProcesses,
    isAtBottom,
    hasScrolled,
    scrollToBottom,
    handleScroll,
    handleLoadOlderHistory,
    streamVirtualizer,
    handleMeasureElement,
  } = useChatStreamViewport({
    activeSessionId,
    streamRows,
    loadOlderHistory,
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

  return (
    <div className="chat-surface flex h-full min-w-0 flex-col">
      <PageHeader
        leading={
          <Button
            variant="ghost"
            size="sm"
            className="h-8 gap-2 px-2 text-sm"
            aria-label={t("chat.history")}
            aria-expanded={isHistorySheet ? historyOpen : !historyCollapsed}
            onClick={() => {
              if (!isHistorySheet) toggleHistoryCollapsed()
              else setHistoryOpen(true)
            }}
          >
            {isHistorySheet && pendingSessionId ? (
              <IconLoader2 className="size-4 animate-spin motion-reduce:animate-none" />
            ) : (
              <IconLayoutSidebar className="size-4" />
            )}
            <span className="hidden lg:inline">{t("chat.history")}</span>
          </Button>
        }
        title={t("navigation.chat")}
        className={`transition-shadow ${
          hasScrolled ? "shadow-xs" : "shadow-none"
        }`}
        titleExtra={
          hasAvailableModels && (
            <ModelSelector
              defaultModelName={defaultModelName}
              apiKeyModels={apiKeyModels}
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
          onClick={handleNewChat}
          className="h-8 gap-2 px-3 text-sm font-medium"
          aria-label={t("chat.newChat")}
        >
          <IconPlus className="size-3.5" />
          <span className="hidden sm:inline">{t("chat.newChat")}</span>
        </Button>
      </PageHeader>
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <SessionHistoryMenu
          activeSessionId={activeSessionId}
          collapsed={historyCollapsed}
          open={historyOpen}
          onOpenChange={setHistoryOpen}
          onSwitchSession={switchSession}
          onDeletedActiveSession={handleNewChat}
          onNewChatInProject={handleNewChatInProject}
        />
        <div className="relative flex min-w-0 flex-1 flex-col">
          {firstReplyReady && (
            <div className="border-border flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2 text-xs">
              <span className="mr-auto">{t("firstUse.replyReady")}</span>
              <Button asChild size="sm" variant="outline">
                <Link to="/channels" onClick={dismissFirstUse}>
                  {t("firstUse.connectChannel")}
                </Link>
              </Button>
              <Button size="sm" variant="ghost" onClick={dismissFirstUse}>
                {t("firstUse.done")}
              </Button>
            </div>
          )}
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
            onPointerDown={(event) => {
              if (
                event.clientX >=
                event.currentTarget.getBoundingClientRect().left +
                  event.currentTarget.clientWidth
              )
                autoScrollEnabledRef.current = false
            }}
            onWheel={(event) => {
              if (event.deltaY < 0) autoScrollEnabledRef.current = false
            }}
            onTouchMove={() => {
              autoScrollEnabledRef.current = false
            }}
            onKeyDown={(event) => {
              if (["ArrowUp", "PageUp", "Home"].includes(event.key))
                autoScrollEnabledRef.current = false
            }}
            className="min-h-0 flex-1 overflow-y-auto px-4 py-6 md:px-8"
          >
            <div
              ref={streamContentRef}
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
                    ref={handleMeasureElement}
                    data-index={virtualRow.index}
                    role="listitem"
                    className="absolute top-0 left-0 w-full pb-6"
                    style={{ transform: `translateY(${virtualRow.start}px)` }}
                  >
                    <ChatStreamRowContent
                      row={row}
                      isLoadingOlderHistory={isLoadingOlderHistory}
                      onLoadOlder={() => void handleLoadOlderHistory()}
                      onRetryHistory={() => void retryHistory()}
                      modelsLoading={modelsLoading}
                      modelsError={modelsError}
                      hasDefaultModel={hasDefaultModel}
                      hasAvailableModels={hasAvailableModels}
                      defaultModelName={defaultModelName}
                      inputDisabledReason={inputDisabledReason}
                      onSelectPrompt={(prompt) => setInput(prompt)}
                      branchGroup={
                        row.kind === "reply" && row.lastText
                          ? branchByVersion.get(row.lastText.id)
                          : undefined
                      }
                      onFeedback={(messageId, value) =>
                        void saveMessageFeedback(messageId, value)
                      }
                      onRegenerate={handleRegenerateMessage}
                      onSelectBranch={selectChatBranch}
                      messageActionsDisabled={messageActionsDisabled}
                      isBranchSwitching={isBranchSwitching}
                      streamingMessageId={streamingMessageId}
                      onEditMessage={handleEditMessage}
                      connectionState={connectionState}
                      onRetryMessage={retryChatMessage}
                      activeTurn={activeTurn}
                      recentActivities={recentActivities}
                      waitingForApproval={Boolean(pendingApproval)}
                      expandedProcesses={expandedProcesses}
                      onToggleProcess={(messageId) =>
                        setExpandedProcesses((previous) => {
                          const next = {
                            ...previous,
                            [messageId]: !(previous[messageId] ?? false),
                          }
                          saveSessionViewport(activeSessionId, {
                            expandedProcesses: next,
                          })
                          return next
                        })
                      }
                    />{" "}
                  </div>
                )
              })}
            </div>
          </div>

          {!isAtBottom && (
            <ChatScrollToBottom onClick={() => scrollToBottom("smooth")} />
          )}
          <input
            ref={fileInputRef}
            type="file"
            accept={CHAT_ATTACHMENT_ACCEPT || undefined}
            multiple
            className="hidden"
            onChange={handleImageSelection}
          />

          <ChatTurnControls
            plan={activeTurn?.plan}
            approval={pendingApproval}
            interrupted={Boolean(
              interrupted && !activeTurn && !isTyping && canInput,
            )}
            historyTurn={historyTurn}
            onContinue={() =>
              sendMessage({ content: "", taskAction: "resume_answer" })
            }
            onRespond={respondToApproval}
          />
          <ChatComposer
            textareaRef={composerTextareaRef}
            input={input}
            attachments={attachments}
            onInputChange={setInput}
            onAddAttachments={handleAddAttachments}
            onPaste={handleComposerPaste}
            onDragEnter={handleComposerDragEnter}
            onDragLeave={handleComposerDragLeave}
            onDragOver={handleComposerDragOver}
            onDrop={handleComposerDrop}
            onRemoveAttachment={handleRemoveAttachment}
            onRetryAttachment={handleRetryAttachment}
            onSend={handleSend}
            onStop={() => sendMessage({ content: "", taskAction: "stop" })}
            isRunning={Boolean(activeTurn) || isTyping}
            onContextDetail={() => {
              sendMessage({ content: "/context", attachments: [] })
            }}
            inputDisabledReason={composerDisabledReason}
            canSend={canSubmit}
            isDragActive={isDragActive}
            contextUsage={contextUsage}
            thinkingLevel={thinkingLevel}
            onThinkingLevelChange={setThinkingLevel}
            editing={editingMessageId !== null}
            onCancelEdit={cancelEditingMessage}
          />
        </div>
      </div>
    </div>
  )
}
