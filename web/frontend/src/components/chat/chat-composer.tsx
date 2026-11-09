import {
  IconArrowUp,
  IconPaperclip,
  IconPlayerStopFilled,
  IconX,
} from "@tabler/icons-react"
import {
  type ClipboardEvent as ReactClipboardEvent,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  useRef,
} from "react"
import { useTranslation } from "react-i18next"
import TextareaAutosize from "react-textarea-autosize"

import { ContextUsageRing } from "@/components/chat/context-usage-ring"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { ChatAttachment, ContextUsage } from "@/store/chat"

export type ChatInputDisabledReason =
  | "gatewayUnknown"
  | "gatewayStarting"
  | "gatewayRestarting"
  | "gatewayStopping"
  | "gatewayStopped"
  | "gatewayError"
  | "websocketConnecting"
  | "websocketDisconnected"
  | "websocketError"
  | "noDefaultModel"
  | "modelsLoading"
  | "modelsError"

interface ChatComposerProps {
  input: string
  attachments: ChatAttachment[]
  onInputChange: (value: string) => void
  onAddImages: () => void
  onPaste: (event: ReactClipboardEvent<HTMLTextAreaElement>) => void
  onDragEnter: (event: ReactDragEvent<HTMLDivElement>) => void
  onDragLeave: (event: ReactDragEvent<HTMLDivElement>) => void
  onDragOver: (event: ReactDragEvent<HTMLDivElement>) => void
  onDrop: (event: ReactDragEvent<HTMLDivElement>) => void
  onRemoveAttachment: (index: number) => void
  onSend: () => void
  onStop: () => void
  isRunning: boolean
  onContextDetail?: () => void
  inputDisabledReason: ChatInputDisabledReason | null
  canSend: boolean
  isDragActive: boolean
  contextUsage?: ContextUsage
}

export function ChatComposer({
  input,
  attachments,
  onInputChange,
  onAddImages,
  onPaste,
  onDragEnter,
  onDragLeave,
  onDragOver,
  onDrop,
  onRemoveAttachment,
  onSend,
  onStop,
  isRunning,
  onContextDetail,
  inputDisabledReason,
  canSend,
  isDragActive,
  contextUsage,
}: ChatComposerProps) {
  const { t } = useTranslation()
  const canInput = inputDisabledReason === null
  const composingRef = useRef(false)
  const disabledMessage =
    inputDisabledReason === null
      ? null
      : t(`chat.disabledPlaceholder.${inputDisabledReason}`)
  const placeholder = disabledMessage ?? t("chat.placeholder")

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    const nativeEvent = e.nativeEvent as Event & {
      isComposing?: boolean
      keyCode?: number
    }
    if (
      composingRef.current ||
      nativeEvent.isComposing ||
      nativeEvent.keyCode === 229
    ) {
      return
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      onSend()
    }
  }

  return (
    <div className="relative z-20 shrink-0 px-4 pb-3 md:px-8">
      <div className="mx-auto flex w-full max-w-3xl flex-col">
        <div
          className={cn(
            "border-border/80 focus-within:border-muted-foreground/60 relative flex w-full flex-col rounded-[28px] border bg-[var(--chat-composer)] shadow-[0_4px_20px_-4px_rgba(0,0,0,0.06)] transition-[background-color,border-color,box-shadow] duration-150 ease-out dark:shadow-[0_4px_24px_-4px_rgba(0,0,0,0.3)]",
            isDragActive &&
              "border-accent/80 bg-accent/5 ring-accent/20 ring-2",
          )}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          {isDragActive && (
            <div className="border-accent/60 bg-card/95 pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-[28px] border-2 border-dashed">
              <div className="text-foreground font-mono text-xs font-medium">
                {t("chat.dropImagesActive")}
              </div>
            </div>
          )}

          {/* Attachments preview row */}
          {attachments.length > 0 && (
            <div className="border-border/40 flex flex-wrap gap-2 border-b p-3">
              {attachments.map((attachment, index) => (
                <div
                  key={`${attachment.url}-${index}`}
                  className="bg-muted border-border relative size-14 overflow-hidden rounded-xl border shadow-xs"
                >
                  <img
                    src={attachment.url}
                    alt={attachment.filename || t("chat.uploadedImage")}
                    className="size-full object-cover"
                  />
                  <button
                    type="button"
                    onClick={() => onRemoveAttachment(index)}
                    className="bg-background/90 text-foreground hover:bg-background focus-visible:ring-ring absolute top-1 right-1 inline-flex size-5 items-center justify-center rounded-full border shadow-xs focus-visible:ring-2 focus-visible:outline-none"
                    aria-label={t("chat.removeImage")}
                  >
                    <IconX className="size-2.5" />
                  </button>
                </div>
              ))}
            </div>
          )}

          {/* Input text area */}
          <TextareaAutosize
            value={input}
            onChange={(e) => onInputChange(e.target.value)}
            onCompositionStart={() => {
              composingRef.current = true
            }}
            onCompositionEnd={() => {
              composingRef.current = false
            }}
            onPaste={onPaste}
            onKeyDown={handleKeyDown}
            placeholder={placeholder}
            disabled={!canInput}
            title={disabledMessage || undefined}
            className={cn(
              "placeholder:text-muted-foreground/50 max-h-[220px] min-h-[48px] resize-none border-0 bg-transparent px-5 pt-3.5 pb-2 text-[15px] leading-relaxed shadow-none focus-visible:outline-none dark:bg-transparent",
              !canInput && "cursor-not-allowed opacity-60",
            )}
            minRows={1}
            maxRows={8}
          />

          {/* Action bar bottom */}
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-1.5">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="text-muted-foreground hover:text-foreground size-8 rounded-full transition-colors"
                onClick={onAddImages}
                disabled={!canInput}
                aria-label={t("chat.attachImage")}
                title={t("chat.attachImage")}
              >
                <IconPaperclip className="size-4" />
              </Button>
            </div>

            <div className="flex items-center gap-2">
              {contextUsage && (
                <ContextUsageRing
                  usage={contextUsage}
                  onDetailClick={onContextDetail}
                />
              )}
              {isRunning && canInput ? (
                <button
                  type="button"
                  onClick={onStop}
                  className="bg-foreground text-background hover:bg-foreground/90 flex size-8 items-center justify-center rounded-full transition-all active:scale-95"
                  aria-label={t("chat.stopTask")}
                  title={t("chat.stopTask")}
                >
                  <IconPlayerStopFilled className="size-3.5" />
                </button>
              ) : (
                canInput && (
                  <button
                    type="button"
                    onClick={onSend}
                    disabled={!canSend}
                    aria-label={t("chat.sendMessage")}
                    className={cn(
                      "flex size-8 items-center justify-center rounded-full transition-all duration-150 active:scale-95",
                      canSend
                        ? "bg-foreground text-background hover:bg-foreground/90 cursor-pointer shadow-xs"
                        : "bg-muted text-muted-foreground/35 cursor-not-allowed",
                    )}
                  >
                    <IconArrowUp className="size-4 stroke-[2.5]" />
                  </button>
                )
              )}
            </div>
          </div>
        </div>

        {/* Minimal ChatGPT-like subtle disclaimer at the bottom */}
        <p className="text-muted-foreground/60 pt-2 text-center text-[11px] select-none">
          {t("chat.composerDisclaimer")}
        </p>
      </div>
    </div>
  )
}
