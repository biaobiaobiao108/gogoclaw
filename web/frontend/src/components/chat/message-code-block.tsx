import {
  IconCheck,
  IconChevronDown,
  IconCopy,
  IconTextWrap,
} from "@tabler/icons-react"
import hljs from "highlight.js/lib/core"
import json from "highlight.js/lib/languages/json"
import { useAtom } from "jotai"
import {
  type ComponentProps,
  type CSSProperties,
  type ReactNode,
  useState,
} from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { cn } from "@/lib/utils"
import { codeBlockWrapAtom } from "@/store/code-block"

import {
  extractCodeBlockFromPreNode,
  extractCodeBlockRenderState,
  type MarkdownNode,
  splitCodeIntoLines,
  splitHighlightedHtmlIntoLines,
  splitRenderedCodeContentIntoLines,
  trimTrailingEmptyRenderedCodeLine,
  trimTrailingEmptyStringLine,
} from "./message-code-block.utils"

hljs.registerLanguage("json", json)

interface MessageCodeBlockProps {
  code: string
  language?: string | null
  label?: string
  className?: string
  bodyClassName?: string
  children?: ReactNode
  trimTrailingEmptyLine?: boolean
}

interface MarkdownCodeBlockProps extends ComponentProps<"pre"> {
  node?: MarkdownNode
}

function getHighlightedHtml(code: string, language?: string | null) {
  if (!language) {
    return null
  }

  try {
    return hljs.highlight(code, { language }).value
  } catch {
    return null
  }
}

export function MessageCodeBlock({
  code,
  language = null,
  label,
  className,
  bodyClassName,
  children,
  trimTrailingEmptyLine = false,
}: MessageCodeBlockProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const [wrapLongLines, setWrapLongLines] = useAtom(codeBlockWrapAtom)
  const [isExpanded, setIsExpanded] = useState(true)
  const blockLabel =
    label ??
    (language
      ? language.toLocaleLowerCase()
      : t("chat.codeLabel").toLocaleLowerCase())
  const copyLabel = isCopied ? t("chat.copiedLabel") : t("chat.copyCode")
  const expandLabel = isExpanded ? t("chat.collapseCode") : t("chat.expandCode")
  const wrapLabel = wrapLongLines
    ? t("chat.disableCodeWrap")
    : t("chat.enableCodeWrap")
  const renderedCodeState = children
    ? extractCodeBlockRenderState(children)
    : {
        renderedContent: null,
        className: undefined,
      }
  const highlightedHtml = !children ? getHighlightedHtml(code, language) : null
  const highlightedLines = highlightedHtml
    ? splitHighlightedHtmlIntoLines(highlightedHtml)
    : null
  const codeLines = children
    ? trimTrailingEmptyLine
      ? trimTrailingEmptyRenderedCodeLine(
          splitRenderedCodeContentIntoLines(renderedCodeState.renderedContent),
        )
      : splitRenderedCodeContentIntoLines(renderedCodeState.renderedContent)
    : trimTrailingEmptyLine
      ? trimTrailingEmptyStringLine(
          highlightedLines ?? splitCodeIntoLines(code),
        )
      : (highlightedLines ?? splitCodeIntoLines(code))
  const lineNumberWidth = `${String(codeLines.length).length + 1}ch`

  return (
    <div
      data-gogoclaw-code-block=""
      className={cn(
        "not-prose border-border/80 bg-muted/20 text-foreground my-3 overflow-hidden rounded-lg border shadow-xs",
        className,
      )}
    >
      {/* Precision terminal header bar */}
      <div className="border-border/60 bg-muted/40 flex items-center justify-between gap-2 border-b px-3 py-1.5">
        <div className="flex items-center gap-2">
          <span className="bg-border size-2 rounded-full" />
          <span className="text-muted-foreground font-mono text-xs font-medium">
            {blockLabel}
          </span>
        </div>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="text-muted-foreground hover:text-foreground h-6 gap-1 px-1.5 text-xs"
            onClick={() => void copy(code)}
            aria-label={copyLabel}
            title={copyLabel}
          >
            {isCopied ? (
              <IconCheck className="text-success size-3.5" />
            ) : (
              <IconCopy className="size-3.5" />
            )}
            <span className="hidden font-mono text-[11px] sm:inline">
              {copyLabel}
            </span>
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className={cn(
              "text-muted-foreground hover:text-foreground h-6 px-1.5 text-xs",
              wrapLongLines && "text-foreground bg-muted",
            )}
            onClick={() => setWrapLongLines((current) => !current)}
            aria-pressed={wrapLongLines}
            aria-label={wrapLabel}
            title={wrapLabel}
          >
            <IconTextWrap className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="text-muted-foreground hover:text-foreground h-6 px-1.5 text-xs"
            onClick={() => setIsExpanded((expanded) => !expanded)}
            aria-expanded={isExpanded}
            aria-label={expandLabel}
            title={expandLabel}
          >
            <IconChevronDown
              className={cn(
                "size-3.5 transition-transform duration-200",
                isExpanded && "rotate-180",
              )}
            />
          </Button>
        </div>
      </div>

      {isExpanded && (
        <pre
          className={cn(
            "bg-card/60 m-0 overflow-x-auto p-3 font-mono text-xs leading-relaxed",
            bodyClassName,
          )}
        >
          <code
            className={cn(
              "block bg-transparent p-0 text-inherit",
              children
                ? renderedCodeState.className
                : cn(
                    highlightedHtml && "hljs",
                    language && `language-${language}`,
                  ),
            )}
          >
            {codeLines.map((line, index) => (
              <span
                key={`${index}-${line.length}`}
                className="grid grid-cols-[var(--code-line-number-width)_minmax(0,1fr)] items-start gap-x-3"
                style={
                  {
                    "--code-line-number-width": lineNumberWidth,
                  } as CSSProperties
                }
              >
                <span className="bg-card/60 text-muted-foreground/40 sticky left-0 z-1 text-right font-mono text-[11px] select-none">
                  {index + 1}
                </span>
                {!children && highlightedLines ? (
                  <span
                    className={cn(
                      "min-w-0 font-mono text-xs",
                      wrapLongLines
                        ? "break-words whitespace-pre-wrap"
                        : "whitespace-pre",
                    )}
                    dangerouslySetInnerHTML={{ __html: line }}
                  />
                ) : (
                  <span
                    className={cn(
                      "min-w-0 font-mono text-xs",
                      wrapLongLines
                        ? "break-words whitespace-pre-wrap"
                        : "whitespace-pre",
                    )}
                  >
                    {line}
                  </span>
                )}
              </span>
            ))}
          </code>
        </pre>
      )}
    </div>
  )
}

export function MarkdownCodeBlock({
  children,
  className,
  node,
}: MarkdownCodeBlockProps) {
  const { code, language } = extractCodeBlockFromPreNode(node)

  return (
    <MessageCodeBlock
      code={code}
      language={language}
      bodyClassName={className}
      trimTrailingEmptyLine
    >
      {children}
    </MessageCodeBlock>
  )
}
