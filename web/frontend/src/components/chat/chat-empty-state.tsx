import {
  IconPlugConnectedX,
  IconRobot,
  IconRobotOff,
  IconStar,
} from "@tabler/icons-react"
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
      <div className="flex flex-col items-center justify-center py-24 text-center">
        <div className="border-warning/20 bg-warning/10 text-warning mb-4 flex size-12 items-center justify-center rounded-lg border">
          <IconRobotOff className="size-6" />
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
      <div className="flex flex-col items-center justify-center py-24 text-center">
        <div className="border-warning/20 bg-warning/10 text-warning mb-4 flex size-12 items-center justify-center rounded-lg border">
          <IconStar className="size-6" />
        </div>
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {t("chat.empty.noSelectedModel")}
        </h2>
        <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
          {t("chat.empty.noSelectedModelDescription")}
        </p>
      </div>
    )
  }

  if (!isConnected) {
    return (
      <div className="flex flex-col items-center justify-center py-24 text-center">
        <div className="border-border bg-muted/30 text-muted-foreground mb-4 flex size-12 items-center justify-center rounded-lg border">
          <IconPlugConnectedX className="size-6" />
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

  return (
    <div className="flex flex-col items-center justify-center py-24 text-center">
      <div className="border-border bg-muted/40 text-foreground mb-4 flex size-12 items-center justify-center rounded-lg border">
        <IconRobot className="size-6 opacity-80" />
      </div>
      <h2 className="text-foreground text-base font-semibold tracking-tight">
        {t("chat.welcome")}
      </h2>
      <p className="text-muted-foreground mt-1 max-w-sm text-xs leading-relaxed">
        {t("chat.welcomeDesc")}
      </p>
    </div>
  )
}
