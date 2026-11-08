import {
  IconBrain,
  IconCheck,
  IconChevronDown,
  IconCopy,
  IconDownload,
  IconFileText,
  IconTerminal2,
} from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import ReactMarkdown from "react-markdown"
import rehypeHighlight from "rehype-highlight"
import rehypeRaw from "rehype-raw"
import rehypeSanitize from "rehype-sanitize"
import remarkGfm from "remark-gfm"

import {
  MarkdownCodeBlock,
  MessageCodeBlock,
} from "@/components/chat/message-code-block"
import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { useSmoothStreamText } from "@/hooks/use-smooth-stream-text"
import { cn } from "@/lib/utils"
import {
  type AssistantMessageKind,
  type ChatAttachment,
  type ChatToolCall,
} from "@/store/chat"

interface AssistantMessageProps {
  detailOpen?: boolean
  content: string
  attachments?: ChatAttachment[]
  kind?: AssistantMessageKind
  modelName?: string
  toolCalls?: ChatToolCall[]
  timestamp?: string | number
  isStreaming?: boolean
}

export function AssistantMessage({
  detailOpen = false,
  content,
  attachments = [],
  kind = "normal",
  modelName,
  toolCalls = [],
  timestamp = "",
  isStreaming = false,
}: AssistantMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const isThought = kind === "thought"
  const isToolCalls = kind === "tool_calls"
  const isCollapsedBlock = isThought || isToolCalls
  const hasText = content.trim().length > 0
  const hasToolCalls = toolCalls.length > 0
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const fileAttachments = attachments.filter(
    (attachment) => attachment.type !== "image",
  )
  const [isExpanded, setIsExpanded] = useState(detailOpen)
  const visibleContent = useSmoothStreamText(content, isStreaming)
  const hasVisibleText = visibleContent.trim().length > 0

  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""
  const collapsedLabel = isThought
    ? isStreaming
      ? t("chat.reasoningStreamingLabel")
      : t("chat.reasoningLabel")
    : t("chat.toolCallsLabel")
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const trimmedModelName = modelName?.trim() ?? ""

  return (
    <div className="group relative flex w-full flex-col gap-1.5 py-1">
      {/* Meta header */}
      {!isCollapsedBlock && (
        <div className="chat-message-meta flex items-center justify-between gap-2 px-0.5 text-xs">
          <div className="text-muted-foreground/80 flex items-center gap-1.5 font-mono text-[11px]">
            <span className="text-foreground/90 font-semibold">GoGoClaw</span>
            {trimmedModelName && (
              <>
                <span className="text-muted-foreground/40">/</span>
                <span>{trimmedModelName}</span>
              </>
            )}
            {formattedTimestamp && (
              <>
                <span className="text-muted-foreground/60">/</span>
                <span className="text-muted-foreground">
                  {formattedTimestamp}
                </span>
              </>
            )}
          </div>

          {hasText && (
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground h-6 w-6 opacity-100 transition-opacity focus-visible:opacity-100 sm:opacity-0 sm:group-focus-within:opacity-100 sm:group-hover:opacity-100"
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

      {/* Main Content Area */}
      {(hasText || isCollapsedBlock || hasToolCalls) && (
        <div
          className={cn(
            "relative w-full rounded-lg text-sm",
            isCollapsedBlock
              ? "text-muted-foreground"
              : "text-foreground leading-relaxed",
          )}
        >
          {/* Thought or Tool calls collapsible trigger */}
          {isCollapsedBlock && (
            <button
              type="button"
              className="hover:bg-muted/40 flex w-full items-center justify-between px-3 py-2 font-mono text-xs transition-colors"
              onClick={() => setIsExpanded(!isExpanded)}
              aria-expanded={isExpanded}
            >
              <div className="flex items-center gap-2">
                {isThought ? (
                  <IconBrain className="text-muted-foreground/80 size-3.5" />
                ) : (
                  <IconTerminal2 className="text-muted-foreground/80 size-3.5" />
                )}
                <span className="font-medium">{collapsedLabel}</span>
                {hasToolCalls && (
                  <span className="bg-muted py-0.2 text-muted-foreground rounded px-1.5 font-mono text-[10px]">
                    {toolCalls.length} calls
                  </span>
                )}
              </div>
              <IconChevronDown
                className={cn(
                  "text-muted-foreground size-3.5 transition-transform duration-200",
                  isExpanded ? "rotate-180" : "",
                )}
              />
            </button>
          )}

          {/* Tool Calls Content */}
          {isCollapsedBlock && isExpanded && isToolCalls && hasToolCalls && (
            <div className="border-border/40 space-y-2.5 border-t p-3">
              {toolCalls.map((toolCall, index) => {
                const explanation =
                  toolCall.extraContent?.toolFeedbackExplanation?.trim() ?? ""
                const toolName = toolCall.function?.name?.trim() ?? ""
                const toolArguments = toolCall.function?.arguments?.trim() ?? ""

                return (
                  <div
                    key={toolCall.id ?? `${toolName}-${index}`}
                    className="space-y-1.5"
                  >
                    {toolName && (
                      <div className="text-foreground/80 flex items-center gap-1.5 font-mono text-xs">
                        <span className="text-success font-bold">❯</span>
                        <span className="font-semibold">{toolName}</span>
                      </div>
                    )}
                    {explanation && (
                      <p className="text-muted-foreground text-xs leading-relaxed">
                        {explanation}
                      </p>
                    )}
                    {toolArguments && (
                      <MessageCodeBlock
                        code={toolArguments}
                        language="json"
                        className="border-border/50 my-1 text-xs"
                        bodyClassName="p-2.5 leading-snug"
                      />
                    )}
                  </div>
                )
              })}
            </div>
          )}

          {/* Thought Content */}
          {isCollapsedBlock && isExpanded && isThought && hasText && (
            <div className="border-border/40 text-muted-foreground border-t p-3 font-mono text-xs leading-relaxed">
              <div className="whitespace-pre-wrap">
                {visibleContent}
                {isStreaming && (
                  <span className="streaming-caret ml-0.5" aria-hidden="true" />
                )}
              </div>
            </div>
          )}

          {/* Normal Assistant Markdown Text */}
          {!isCollapsedBlock && hasVisibleText && (
            <div className="prose dark:prose-invert max-w-none text-base leading-relaxed [overflow-wrap:anywhere] break-words">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                rehypePlugins={[rehypeRaw, rehypeSanitize, rehypeHighlight]}
                components={{
                  pre: MarkdownCodeBlock,
                }}
              >
                {visibleContent}
              </ReactMarkdown>
              {isStreaming && (
                <span className="streaming-caret ml-0.5" aria-hidden="true" />
              )}
            </div>
          )}
        </div>
      )}

      {/* Image Attachments */}
      {imageAttachments.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {imageAttachments.map((attachment, index) => (
            <a
              key={`${attachment.url}-${index}`}
              href={attachment.url}
              target="_blank"
              rel="noreferrer"
              className="border-border/60 hover:border-border relative overflow-hidden rounded-lg border shadow-xs transition-opacity hover:opacity-95"
            >
              <img
                src={attachment.url}
                alt={attachment.filename || "Attached image"}
                className="max-h-80 max-w-[280px] object-contain"
              />
            </a>
          ))}
        </div>
      )}

      {/* File Attachments */}
      {fileAttachments.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {fileAttachments.map((attachment, index) => (
            <a
              key={`${attachment.url}-${index}`}
              href={attachment.url}
              download={attachment.filename}
              className="border-border/60 bg-card hover:bg-muted/40 flex w-fit max-w-sm min-w-[200px] items-center gap-3 rounded-lg border px-3 py-2 text-xs transition-colors"
            >
              <IconFileText className="text-muted-foreground h-4 w-4" />
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="text-foreground truncate font-medium">
                  {attachment.filename || "Download file"}
                </span>
                <span className="text-muted-foreground font-mono text-[10px]">
                  {attachment.filename?.split(".").pop()?.toUpperCase() ||
                    "FILE"}
                </span>
              </div>
              <IconDownload className="text-muted-foreground h-3.5 w-3.5" />
            </a>
          ))}
        </div>
      )}
    </div>
  )
}
