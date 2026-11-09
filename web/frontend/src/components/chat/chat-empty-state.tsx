import {
  IconCode,
  IconCpu,
  IconFileSearch,
  IconPlugConnectedX,
  IconRobotOff,
  IconSparkles,
  IconTestPipe,
} from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"

interface ChatEmptyStateProps {
  hasAvailableModels: boolean
  defaultModelName: string
  isConnected: boolean
  onSelectPrompt?: (prompt: string) => void
}

const PROMPT_ICONS = [IconFileSearch, IconTestPipe, IconCpu, IconCode]

export function ChatEmptyState({
  hasAvailableModels,
  defaultModelName,
  isConnected,
  onSelectPrompt,
}: ChatEmptyStateProps) {
  const { t } = useTranslation()

  if (!hasAvailableModels) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <div className="bg-warning/10 text-warning mb-3 flex size-12 items-center justify-center rounded-2xl">
          <IconRobotOff className="size-6" aria-hidden="true" />
        </div>
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
        <div className="bg-muted text-muted-foreground mb-3 flex size-12 items-center justify-center rounded-2xl">
          <IconSparkles className="size-6" aria-hidden="true" />
        </div>
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

  if (!isConnected) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <div className="bg-muted text-muted-foreground mb-3 flex size-12 items-center justify-center rounded-2xl">
          <IconPlugConnectedX className="size-6" aria-hidden="true" />
        </div>
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {t("chat.empty.notRunning")}
        </h2>
        <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
          {t("chat.empty.notRunningDescription")}
        </p>
      </div>
    )
  }

  const promptExamples =
    (t("chat.promptExamples", { returnObjects: true }) as
      Array<{ label: string; prompt: string }> | undefined) ?? []

  return (
    <div className="flex flex-col items-center justify-center py-12 text-center md:py-20">
      <div className="bg-foreground text-background mb-5 flex size-12 items-center justify-center rounded-2xl shadow-xs">
        <IconSparkles className="size-6" />
      </div>
      <h2 className="text-foreground text-2xl font-semibold tracking-tight md:text-3xl">
        {t("chat.welcome")}
      </h2>
      <p className="text-muted-foreground mt-2 max-w-md text-sm leading-relaxed">
        {t("chat.welcomeDesc")}
      </p>

      {Array.isArray(promptExamples) &&
        promptExamples.length > 0 &&
        onSelectPrompt && (
          <div className="mt-8 grid w-full max-w-xl grid-cols-1 gap-2.5 sm:grid-cols-2">
            {promptExamples.map((item, idx) => {
              const Icon = PROMPT_ICONS[idx % PROMPT_ICONS.length]
              return (
                <button
                  key={item.label}
                  type="button"
                  onClick={() => onSelectPrompt(item.prompt)}
                  className="group border-border/70 hover:border-border hover:bg-muted/40 bg-card/60 flex items-start gap-3 rounded-2xl border p-3.5 text-left transition-all duration-150 hover:shadow-xs active:scale-[0.99]"
                >
                  <div className="bg-muted/80 text-muted-foreground group-hover:text-foreground group-hover:bg-muted flex size-8 shrink-0 items-center justify-center rounded-xl transition-colors">
                    <Icon className="size-4" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-foreground text-xs font-medium tracking-tight">
                      {item.label}
                    </div>
                    <div className="text-muted-foreground mt-0.5 line-clamp-1 text-[11px] leading-normal">
                      {item.prompt}
                    </div>
                  </div>
                </button>
              )
            })}
          </div>
        )}
    </div>
  )
}
