import { IconPlugConnectedX, IconRobotOff } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"

interface ChatEmptyStateProps {
  hasAvailableModels: boolean
  defaultModelName: string
  isConnected: boolean
}

export function ChatEmptyState({
  hasAvailableModels,
  defaultModelName,
  isConnected,
}: ChatEmptyStateProps) {
  const { t } = useTranslation()

  if (!hasAvailableModels) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <h2 className="text-foreground flex items-center gap-2 text-base font-semibold tracking-tight">
          <IconRobotOff className="text-warning size-4" aria-hidden="true" />
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
        <h2 className="text-foreground flex items-center gap-2 text-base font-semibold tracking-tight">
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
        <h2 className="text-foreground flex items-center gap-2 text-base font-semibold tracking-tight">
          <IconPlugConnectedX
            className="text-muted-foreground size-4"
            aria-hidden="true"
          />
          {t("chat.empty.notRunning")}
        </h2>
        <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
          {t("chat.empty.notRunningDescription")}
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col items-center justify-center py-20 text-center">
      <h2 className="text-foreground text-base font-semibold tracking-tight">
        {t("chat.welcome")}
      </h2>
      <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
        {t("chat.welcomeDesc")}
      </p>
    </div>
  )
}
