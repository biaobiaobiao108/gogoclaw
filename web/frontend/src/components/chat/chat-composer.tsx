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
    <div className="relative z-20 shrink-0 px-4 pb-4 md:px-8 lg:px-24 xl:px-48">
      <div className="mx-auto flex w-full max-w-4xl flex-col">
        <div
          className={cn(
            "bg-card/90 supports-backdrop-filter:bg-card/75 border-border/80 focus-within:border-primary/50 relative flex w-full flex-col rounded-lg border shadow-xs backdrop-blur-sm transition-[background-color,border-color,box-shadow] duration-[var(--motion-state)] ease-out",
            isDragActive &&
              "border-accent/80 bg-accent/5 ring-accent/20 ring-2",
          )}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          {isDragActive && (
            <div className="border-accent/60 bg-card/95 pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg border-2 border-dashed">
              <div className="text-foreground font-mono text-xs font-medium">
                {t("chat.dropImagesActive")}
              </div>
            </div>
          )}

          {/* Attachments preview row */}
          {attachments.length > 0 && (
            <div className="border-border/40 flex flex-wrap gap-2 border-b p-2.5">
              {attachments.map((attachment, index) => (
                <div
                  key={`${attachment.url}-${index}`}
                  className="bg-muted border-border relative size-14 overflow-hidden rounded-md border"
                >
                  <img
                    src={attachment.url}
                    alt={attachment.filename || t("chat.uploadedImage")}
                    className="size-full object-cover"
                  />
                  <button
                    type="button"
                    onClick={() => onRemoveAttachment(index)}
                    className="bg-background/90 text-foreground hover:bg-background focus-visible:ring-ring absolute top-1 right-1 inline-flex size-6 items-center justify-center rounded-full border shadow-xs focus-visible:ring-2 focus-visible:outline-none"
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
              "placeholder:text-muted-foreground/45 max-h-[220px] min-h-[48px] resize-none border-0 bg-transparent px-3 py-2.5 text-sm leading-relaxed shadow-none focus-visible:outline-none dark:bg-transparent",
              !canInput && "cursor-not-allowed opacity-60",
            )}
            minRows={1}
            maxRows={8}
          />

          {/* Action bar bottom */}
          <div className="flex items-center justify-between px-2.5 py-1.5">
            <div className="flex items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="text-muted-foreground hover:text-foreground size-11 rounded-md md:size-8"
                onClick={onAddImages}
                disabled={!canInput}
                aria-label={t("chat.attachImage")}
                title={t("chat.attachImage")}
              >
                <IconPaperclip className="size-3.5" />
              </Button>
            </div>

            <div className="flex items-center gap-2">
              {contextUsage && (
                <ContextUsageRing
                  usage={contextUsage}
                  onDetailClick={onContextDetail}
                />
              )}
              {isRunning && canInput && (
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-11 rounded-md md:size-8"
                  onClick={onStop}
                  aria-label={t("chat.stopTask")}
                  title={t("chat.stopTask")}
                >
                  <IconPlayerStopFilled className="size-3" />
                </Button>
              )}
              {canInput && (
                <Button
                  type="button"
                  size="icon"
                  className={cn(
                    "size-11 rounded-md transition-[color,background-color,transform] duration-[var(--motion-state)] ease-out active:duration-[var(--motion-press)] md:size-8",
                    canSend
                      ? "bg-primary text-primary-foreground hover:bg-primary/90"
                      : "bg-muted text-muted-foreground/40",
                  )}
                  onClick={onSend}
                  disabled={!canSend}
                  aria-label={t("chat.sendMessage")}
                >
                  <IconArrowUp className="size-3.5" />
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
