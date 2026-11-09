import {
  IconCheck,
  IconCopy,
  IconFileText,
  IconPencil,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { cn } from "@/lib/utils"
import type { ChatAttachment, MessageDeliveryStatus } from "@/store/chat"

interface UserMessageProps {
  content: string
  attachments?: ChatAttachment[]
  timestamp?: string | number
  deliveryStatus?: MessageDeliveryStatus
  onRetry?: () => void
  onEdit?: () => void
}

export function UserMessage({
  content,
  attachments = [],
  timestamp = "",
  deliveryStatus,
  onRetry,
  onEdit,
}: UserMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const hasText = content.trim().length > 0
  const isCommand = content.trim().startsWith("/")
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const fileAttachments = attachments.filter(
    (attachment) => attachment.type !== "image",
  )
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""

  return (
    <div className="group flex w-full flex-col items-end gap-1.5 py-1">
      {imageAttachments.length > 0 && (
        <div className="flex max-w-[85%] flex-wrap justify-end gap-2 sm:max-w-[70%]">
          {imageAttachments.map((attachment, index) => (
            <div
              key={`${attachment.url}-${index}`}
              className="border-border/60 overflow-hidden rounded-2xl border shadow-xs"
            >
              <img
                src={attachment.url}
                alt={attachment.filename || t("chat.uploadedImage")}
                className="max-h-72 max-w-full object-cover"
              />
            </div>
          ))}
        </div>
      )}

      {fileAttachments.length > 0 && (
        <div className="flex max-w-[85%] flex-wrap justify-end gap-2 sm:max-w-[70%]">
          {fileAttachments.map((attachment, index) => (
            <a
              key={`${attachment.url}-${index}`}
              href={attachment.url}
              download={attachment.filename}
              className="border-border/60 bg-card hover:bg-muted/40 flex max-w-full min-w-[180px] items-center gap-2 rounded-lg border px-3 py-2 text-xs transition-colors"
            >
              <IconFileText className="text-muted-foreground size-4 shrink-0" />
              <span className="min-w-0 flex-1 truncate">
                {attachment.filename || t("chat.attachmentFallback")}
              </span>
            </a>
          ))}
        </div>
      )}

      {hasText && (
        <div className="relative max-w-[85%] sm:max-w-[75%]">
          <div
            className={cn(
              "px-4.5 py-3 text-base leading-relaxed break-words whitespace-pre-wrap shadow-xs",
              isCommand
                ? "border-border/80 bg-muted/40 text-foreground rounded-2xl border font-mono text-sm"
                : "text-foreground rounded-[24px] bg-[var(--chat-user)] font-normal",
            )}
          >
            {isCommand ? (
              <div className="flex items-start gap-2">
                <span className="text-success font-bold select-none">❯</span>
                <span className="mt-[1px]">{content}</span>
              </div>
            ) : (
              content
            )}
          </div>
        </div>
      )}

      {(hasText || attachments.length > 0) && (
        <div className="mt-1 flex justify-end gap-1 px-1">
          {onEdit && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground h-6 w-6 rounded-md opacity-100 transition-opacity focus-visible:opacity-100 sm:opacity-0 sm:group-hover:opacity-100"
              onClick={onEdit}
              aria-label={t("chat.editMessage")}
              title={t("chat.editMessage")}
            >
              <IconPencil className="h-3.5 w-3.5" />
            </Button>
          )}
          {hasText && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground h-6 w-6 rounded-md opacity-100 transition-opacity focus-visible:opacity-100 sm:opacity-0 sm:group-hover:opacity-100"
              onClick={() => void copy(content)}
              aria-label={copyMessageLabel}
              title={copyMessageLabel}
            >
              {isCopied ? (
                <IconCheck className="text-success h-3.5 w-3.5" />
              ) : (
                <IconCopy className="h-3.5 w-3.5" />
              )}
            </Button>
          )}
        </div>
      )}

      {(formattedTimestamp || deliveryStatus) && (
        <span className="text-muted-foreground/70 px-2 text-[11px] opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {formattedTimestamp}
          {deliveryStatus === "sending" && <> · {t("chat.deliverySending")}</>}
          {deliveryStatus === "failed" && (
            <span className="text-destructive font-medium">
              {" "}
              · {t("chat.deliveryFailed")}
            </span>
          )}
          {deliveryStatus === "unknown" && <> · {t("chat.deliveryUnknown")}</>}
          {(deliveryStatus === "unknown" || deliveryStatus === "failed") &&
            onRetry && (
              <button
                type="button"
                className="hover:text-foreground ml-2 underline"
                onClick={onRetry}
              >
                {t("chat.deliveryRetry")}
              </button>
            )}
        </span>
      )}
    </div>
  )
}
