import { IconCheck, IconCopy } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { cn } from "@/lib/utils"
import type { ChatAttachment } from "@/store/chat"

interface UserMessageProps {
  content: string
  attachments?: ChatAttachment[]
  timestamp?: string | number
}

export function UserMessage({
  content,
  attachments = [],
  timestamp = "",
}: UserMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const hasText = content.trim().length > 0
  const isCommand = content.trim().startsWith("/")
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""

  return (
    <div className="group flex w-full flex-col items-end gap-1.5">
      {imageAttachments.length > 0 && (
        <div className="flex max-w-[85%] flex-wrap justify-end gap-2 sm:max-w-[70%]">
          {imageAttachments.map((attachment, index) => (
            <div
              key={`${attachment.url}-${index}`}
              className="border-border/80 overflow-hidden rounded-lg border shadow-xs"
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

      {hasText && (
        <div className="relative max-w-[85%] sm:max-w-[75%]">
          <div
            className={cn(
              "rounded-lg px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap",
              isCommand
                ? "border-border/80 bg-muted/40 text-foreground border font-mono"
                : "bg-primary text-primary-foreground selection:bg-accent selection:text-accent-foreground font-normal shadow-xs",
            )}
          >
            {isCommand ? (
              <div className="flex items-start gap-2">
                <span className="font-bold text-emerald-500 select-none">
                  ❯
                </span>
                <span className="mt-[1px]">{content}</span>
              </div>
            ) : (
              content
            )}
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className={cn(
              "absolute top-1.5 right-1.5 h-6 w-6 rounded-md opacity-0 transition-opacity group-hover:opacity-100",
              isCommand
                ? "text-muted-foreground hover:bg-muted"
                : "text-primary-foreground/70 hover:bg-white/10 hover:text-white",
            )}
            onClick={() => void copy(content)}
            aria-label={copyMessageLabel}
            title={copyMessageLabel}
          >
            {isCopied ? (
              <IconCheck className="h-3.5 w-3.5 text-emerald-400" />
            ) : (
              <IconCopy className="h-3.5 w-3.5" />
            )}
          </Button>
        </div>
      )}

      {formattedTimestamp && (
        <span className="text-muted-foreground/60 px-1 font-mono text-[10px]">
          {formattedTimestamp}
        </span>
      )}
    </div>
  )
}
