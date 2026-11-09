import {
  IconBrain,
  IconCheck,
  IconChevronDown,
  IconChevronLeft,
  IconChevronRight,
  IconCopy,
  IconDownload,
  IconExternalLink,
  IconFileText,
  IconRefresh,
  IconTerminal2,
  IconThumbDown,
  IconThumbUp,
} from "@tabler/icons-react"
import { memo, useState } from "react"
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
  type ChatBranchGroup,
  type ChatFeedbackValue,
  type ChatSource,
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
  feedback?: ChatFeedbackValue
  sources?: ChatSource[]
  branchGroup?: ChatBranchGroup
  onFeedback?: (value: ChatFeedbackValue) => void
  onRegenerate?: () => void
  onSelectBranch?: (versionId: string) => void
}

export const AssistantMessage = memo(function AssistantMessage({
  detailOpen = false,
  content,
  attachments = [],
  kind = "normal",
  modelName,
  toolCalls = [],
  timestamp = "",
  isStreaming = false,
  feedback,
  sources = [],
  branchGroup,
  onFeedback,
  onRegenerate,
  onSelectBranch,
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
  const visibleContent = useSmoothStreamText(content, isThought && isStreaming)
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

  return (
    <div className="group relative flex w-full flex-col gap-2 py-2">
      {/* Main Content Area */}
      {(hasText || isCollapsedBlock || hasToolCalls) && (
        <div
          className={cn(
            "relative w-full text-[15px]",
            isCollapsedBlock
              ? "text-muted-foreground"
              : "text-foreground leading-relaxed",
          )}
        >
          {/* Thought or Tool calls collapsible trigger */}
          {isCollapsedBlock && (
            <button
              type="button"
              className="hover:bg-muted/50 bg-muted/20 border-border/60 flex w-fit max-w-full cursor-pointer items-center gap-2 rounded-full border px-3.5 py-1.5 text-xs transition-colors select-none"
              onClick={() => setIsExpanded(!isExpanded)}
              aria-expanded={isExpanded}
            >
              {isThought ? (
                <IconBrain className="text-muted-foreground size-3.5" />
              ) : (
                <IconTerminal2 className="text-muted-foreground size-3.5" />
              )}
              <span className="text-foreground/80 font-medium">
                {collapsedLabel}
              </span>
              {hasToolCalls && (
                <span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 font-mono text-[11px]">
                  {toolCalls.length} calls
                </span>
              )}
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
            <div className="border-border/60 bg-muted/20 mt-2.5 space-y-2.5 rounded-2xl border p-3.5">
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
                      <div className="text-foreground/90 flex items-center gap-1.5 font-mono text-xs">
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
            <div className="border-border/60 bg-muted/20 text-muted-foreground mt-2.5 rounded-2xl border p-4 text-xs leading-relaxed whitespace-pre-wrap">
              {visibleContent}
            </div>
          )}

          {/* Normal Assistant Markdown Text */}
          {!isCollapsedBlock && hasVisibleText && (
            <div className="prose dark:prose-invert max-w-none text-base leading-7 [overflow-wrap:anywhere] break-words">
              <StreamingMarkdown content={content} isStreaming={isStreaming} />
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
              className="border-border/60 hover:border-border relative overflow-hidden rounded-2xl border shadow-xs transition-opacity hover:opacity-95"
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
              className="border-border/60 bg-card hover:bg-muted/40 flex w-fit max-w-sm min-w-[200px] items-center gap-3 rounded-2xl border px-3.5 py-2.5 text-xs transition-colors"
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

      {!isCollapsedBlock && sources.length > 0 && (
        <div className="border-border/60 mt-1 border-t pt-2 text-xs">
          <div className="text-muted-foreground mb-1.5">
            {t("chat.sourcesLabel")}
          </div>
          <ol className="space-y-1">
            {sources.map((source, index) => (
              <li
                key={`${source.url}-${index}`}
                className="flex min-w-0 items-start gap-1.5"
              >
                <span className="text-muted-foreground w-4 shrink-0 text-right tabular-nums">
                  {index + 1}
                </span>
                <a
                  href={source.url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-accent flex min-w-0 items-center gap-1 break-all hover:underline"
                  title={source.snippet || source.url}
                >
                  <span className="min-w-0">{source.title || source.url}</span>
                  <IconExternalLink className="size-3 shrink-0" />
                </a>
              </li>
            ))}
          </ol>
        </div>
      )}

      {/* Bottom Action Row (ChatGPT style: appears under message on hover) */}
      {!isCollapsedBlock && hasText && !isStreaming && (
        <div className="mt-1 flex flex-wrap items-center gap-1 text-xs">
          <Button
            variant="ghost"
            size="icon"
            className="text-muted-foreground hover:text-foreground h-7 w-7 rounded-md opacity-100 transition-opacity focus-visible:opacity-100 sm:opacity-0 sm:group-hover:opacity-100"
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

          {onFeedback && (
            <>
              <Button
                variant="ghost"
                size="icon"
                className={cn(
                  "text-muted-foreground hover:text-foreground h-7 w-7 rounded-md",
                  feedback === "up" && "bg-muted text-foreground",
                )}
                onClick={() => onFeedback("up")}
                aria-label={t("chat.feedbackUp")}
                aria-pressed={feedback === "up"}
                title={t("chat.feedbackUp")}
              >
                <IconThumbUp className="h-3.5 w-3.5" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className={cn(
                  "text-muted-foreground hover:text-foreground h-7 w-7 rounded-md",
                  feedback === "down" && "bg-muted text-foreground",
                )}
                onClick={() => onFeedback("down")}
                aria-label={t("chat.feedbackDown")}
                aria-pressed={feedback === "down"}
                title={t("chat.feedbackDown")}
              >
                <IconThumbDown className="h-3.5 w-3.5" />
              </Button>
            </>
          )}
          {onRegenerate && (
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground h-7 w-7 rounded-md"
              onClick={onRegenerate}
              aria-label={t("chat.regenerate")}
              title={t("chat.regenerate")}
            >
              <IconRefresh className="h-3.5 w-3.5" />
            </Button>
          )}

          {branchGroup && branchGroup.versions.length > 1 && (
            <div className="text-muted-foreground ml-1 flex items-center gap-0.5">
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 rounded-md"
                disabled={
                  branchGroup.activeVersionId === branchGroup.versions[0]?.id
                }
                onClick={() => {
                  const index = branchGroup.versions.findIndex(
                    (version) => version.id === branchGroup.activeVersionId,
                  )
                  const previous = branchGroup.versions[index - 1]
                  if (previous) onSelectBranch?.(previous.id)
                }}
                aria-label={t("chat.previousBranch")}
                title={t("chat.previousBranch")}
              >
                <IconChevronLeft className="h-3.5 w-3.5" />
              </Button>
              <span className="min-w-8 text-center font-mono text-[11px] tabular-nums">
                {Math.max(
                  1,
                  branchGroup.versions.findIndex(
                    (version) => version.id === branchGroup.activeVersionId,
                  ) + 1,
                )}
                {" / "}
                {branchGroup.versions.length}
              </span>
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 rounded-md"
                disabled={
                  branchGroup.versions.at(-1)?.id ===
                  branchGroup.activeVersionId
                }
                onClick={() => {
                  const index = branchGroup.versions.findIndex(
                    (version) => version.id === branchGroup.activeVersionId,
                  )
                  const next = branchGroup.versions[index + 1]
                  if (next) onSelectBranch?.(next.id)
                }}
                aria-label={t("chat.nextBranch")}
                title={t("chat.nextBranch")}
              >
                <IconChevronRight className="h-3.5 w-3.5" />
              </Button>
            </div>
          )}

          {modelName && (
            <span className="text-muted-foreground/50 font-mono text-[11px] opacity-0 transition-opacity group-hover:opacity-100">
              {modelName}
            </span>
          )}

          {formattedTimestamp && (
            <span className="text-muted-foreground/40 text-[11px] opacity-0 transition-opacity group-hover:opacity-100">
              {formattedTimestamp}
            </span>
          )}
        </div>
      )}
    </div>
  )
})

const MarkdownBlock = memo(function MarkdownBlock({
  content,
  highlight,
}: {
  content: string
  highlight: boolean
}) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={
        highlight
          ? [rehypeRaw, rehypeSanitize, rehypeHighlight]
          : [rehypeRaw, rehypeSanitize]
      }
      components={{ pre: MarkdownCodeBlock }}
    >
      {content}
    </ReactMarkdown>
  )
})

function StreamingMarkdown({
  content,
  isStreaming,
}: {
  content: string
  isStreaming: boolean
}) {
  // Keep the complete document for references, lists and tables. Streaming text
  // is parsed only on network updates; syntax highlighting runs on completion.
  return <MarkdownBlock content={content} highlight={!isStreaming} />
}
