import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import type { ChatInputDisabledReason } from "@/components/chat/chat-composer"
import { Button } from "@/components/ui/button"
import { useGateway } from "@/hooks/use-gateway"

interface ChatEmptyStateProps {
  hasAvailableModels: boolean
  defaultModelName: string
  inputDisabledReason: ChatInputDisabledReason | null
  onSelectPrompt?: (prompt: string) => void
}

export function ChatEmptyState({
  hasAvailableModels,
  defaultModelName,
  inputDisabledReason,
  onSelectPrompt,
}: ChatEmptyStateProps) {
  const { t } = useTranslation()
  const { canStart, startReason, start, loading, error } = useGateway()

  if (!hasAvailableModels) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {t("chat.empty.noConfiguredModel")}
        </h2>
        <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
          {t("chat.empty.noConfiguredModelDescription")}
        </p>
        <Button
          asChild
          variant="outline"
          size="sm"
          className="mt-4 h-8 px-3 text-xs"
        >
          <Link to="/models">{t("chat.empty.goToModels")}</Link>
        </Button>
      </div>
    )
  }

  if (!defaultModelName) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {t("chat.empty.noSelectedModel")}
        </h2>
        <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
          {t("chat.empty.noSelectedModelDescription")}
        </p>
        <Button
          asChild
          variant="outline"
          size="sm"
          className="mt-4 h-8 px-3 text-xs"
        >
          <Link to="/models">{t("chat.empty.selectModel")}</Link>
        </Button>
      </div>
    )
  }

  if (inputDisabledReason) {
    const stopped = inputDisabledReason === "gatewayStopped"
    const failed = inputDisabledReason === "gatewayError"
    const detail = error ?? (!canStart ? startReason : undefined)
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {stopped
            ? t("chat.empty.notRunning")
            : t(`chat.disabledPlaceholder.${inputDisabledReason}`)}
        </h2>
        {detail && (stopped || failed) && (
          <p
            role="status"
            className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed break-words"
          >
            {detail}
          </p>
        )}
        {stopped && canStart && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="mt-4"
            disabled={loading}
            onClick={() => void start()}
          >
            {t("header.gateway.action.start")}
          </Button>
        )}
        {stopped && !canStart && (
          <Button asChild variant="outline" size="sm" className="mt-4">
            <Link to="/models">{t("chat.empty.goToModels")}</Link>
          </Button>
        )}
        {failed && (
          <Button asChild variant="outline" size="sm" className="mt-4">
            <Link to="/logs">{t("navigation.logs")}</Link>
          </Button>
        )}
      </div>
    )
  }

  const promptExamples =
    (t("chat.promptExamples", { returnObjects: true }) as
      Array<{ category: string; prompt: string }> | undefined) ?? []

  return (
    <div className="flex flex-col items-center py-12 md:py-20">
      <div className="w-full max-w-2xl">
        <h2 className="text-foreground text-2xl font-semibold tracking-tight md:text-3xl">
          {t("chat.welcome")}
        </h2>

        {Array.isArray(promptExamples) &&
          promptExamples.length > 0 &&
          onSelectPrompt && (
            <ul className="border-border/80 divide-border/80 mt-8 divide-y border-y">
              {promptExamples.map((item) => (
                <li key={item.prompt}>
                  <button
                    type="button"
                    data-ui-align="start"
                    onClick={() => onSelectPrompt(item.prompt)}
                    className="hover:bg-muted/35 focus-visible:bg-muted/35 focus-visible:ring-ring/50 grid w-full grid-cols-[56px_minmax(0,1fr)] items-start gap-3 px-1 py-4 text-left transition-[background-color] duration-[var(--motion-state)] ease-out focus-visible:ring-2 focus-visible:outline-none sm:grid-cols-[88px_minmax(0,1fr)] sm:gap-4 sm:px-3"
                  >
                    <span className="text-muted-foreground text-xs leading-6 font-medium">
                      {item.category}
                    </span>
                    <span className="text-foreground text-sm leading-6 text-pretty">
                      {item.prompt}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
      </div>
    </div>
  )
}
