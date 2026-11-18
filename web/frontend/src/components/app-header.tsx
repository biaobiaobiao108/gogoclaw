import {
  IconBook,
  IconDotsVertical,
  IconLanguage,
  IconLoader2,
  IconLogout,
  IconMenu2,
  IconMoon,
  IconPlayerPlay,
  IconPower,
  IconRefresh,
  IconSun,
} from "@tabler/icons-react"
import { Link, useMatch } from "@tanstack/react-router"
import { useAtomValue } from "jotai"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { postLauncherDashboardLogout } from "@/api/launcher-auth"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog.tsx"
import { Button } from "@/components/ui/button.tsx"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu.tsx"
import { Separator } from "@/components/ui/separator.tsx"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { useGateway } from "@/hooks/use-gateway.ts"
import { useTheme } from "@/hooks/use-theme.ts"
import { cn } from "@/lib/utils"
import { activeSessionTitleAtom } from "@/store/chat"

const DOCS_URL = "https://github.com/biaobiaobiao108/gogoclaw/tree/main/docs"

function ChatSessionTitle() {
  const title = useAtomValue(activeSessionTitleAtom)

  return (
    <div className="col-start-2 row-start-1 truncate text-center text-sm font-semibold tracking-tight">
      {title}
    </div>
  )
}

export function AppHeader() {
  const { i18n, t } = useTranslation()
  const isChat = useMatch({
    from: "/chat",
    shouldThrow: false,
    select: () => true,
  })
  const { theme, toggleTheme } = useTheme()
  const {
    state: gwState,
    loading: gwLoading,
    canStart,
    startReason,
    restartRequired,
    start,
    restart,
    stop,
    error: gwError,
  } = useGateway()

  const isRunning = gwState === "running"
  const isStarting = gwState === "starting"
  const isRestarting = gwState === "restarting"
  const isStopping = gwState === "stopping"
  const isStopped = gwState === "stopped" || gwState === "unknown"
  const transitionLabel = isStopping
    ? t("header.gateway.status.stopping")
    : isRestarting
      ? t("header.gateway.status.restarting")
      : isStarting
        ? t("header.gateway.status.starting")
        : undefined
  const startBlockedReason = !canStart ? startReason : undefined
  const gatewayStatusLabel =
    gwError ??
    startBlockedReason ??
    transitionLabel ??
    (isRunning
      ? t("header.gateway.status.running")
      : gwState !== "unknown"
        ? t("chat.notConnected")
        : undefined)

  const [showStopDialog, setShowStopDialog] = React.useState(false)
  const [showLogoutDialog, setShowLogoutDialog] = React.useState(false)

  const handleLogout = async () => {
    await postLauncherDashboardLogout()
    globalThis.location.assign("/launcher-login")
  }

  const handleGatewayToggle = () => {
    if (gwLoading || isRestarting || isStopping) {
      return
    }
    if (isRunning) {
      setShowStopDialog(true)
    } else {
      void start()
    }
  }

  const handleGatewayRestart = () => {
    if (gwLoading || isRestarting || !restartRequired || !canStart) return
    void restart()
  }

  const confirmStop = () => {
    setShowStopDialog(false)
    stop()
  }

  return (
    <header className="bg-background/80 supports-backdrop-filter:bg-background/60 border-border/40 sticky top-0 z-50 grid h-11 shrink-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 border-b px-3 backdrop-blur-md lg:grid-cols-[minmax(max-content,1fr)_minmax(0,1fr)_minmax(max-content,1fr)]">
      <div className="flex items-center gap-3">
        <SidebarTrigger
          className="text-muted-foreground hover:bg-muted hover:text-foreground flex h-8 w-8 items-center justify-center rounded-md sm:hidden [&>svg]:size-4"
          aria-label={t("header.toggleSidebar")}
        >
          <IconMenu2 />
        </SidebarTrigger>
        <div className="flex shrink-0 items-center gap-1">
          <Link
            to="/chat"
            aria-label={t("header.home")}
            className="-ml-1 flex h-8 items-center gap-2 rounded-md px-1 opacity-95 transition-opacity hover:opacity-100"
          >
            <img className="size-5" src="/public/logo.svg" alt="" />
            <span className="hidden text-sm font-semibold tracking-tight md:inline">
              GoGoClaw
            </span>
          </Link>
          <span id="gateway-status" role="status" className="flex items-center">
            <span className="sr-only">{gatewayStatusLabel}</span>
            <span
              aria-hidden="true"
              className={cn(
                "size-1.5 rounded-full",
                gwError || gwState === "error"
                  ? "bg-destructive"
                  : !canStart || transitionLabel
                    ? "bg-warning"
                    : isRunning
                      ? "bg-success"
                      : "bg-muted-foreground",
              )}
            />
          </span>
        </div>
      </div>

      {isChat && <ChatSessionTitle />}

      <AlertDialog open={showStopDialog} onOpenChange={setShowStopDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("header.gateway.stopDialog.title")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("header.gateway.stopDialog.description")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmStop}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {t("header.gateway.stopDialog.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={showLogoutDialog} onOpenChange={setShowLogoutDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("header.logout.tooltip")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("header.logout.description")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void handleLogout()}>
              {t("header.logout.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <div className="text-muted-foreground col-start-3 row-start-1 flex items-center gap-1 justify-self-end text-sm md:gap-1.5">
        {restartRequired && (
          <Button
            variant="secondary"
            size="icon-sm"
            className="text-warning hover:bg-warning/10 h-8 w-8"
            onClick={handleGatewayRestart}
            disabled={gwLoading || isRestarting || isStopping || !canStart}
            aria-label={`${t("header.gateway.action.restart")}: ${t("header.gateway.restartRequired")}`}
          >
            <IconRefresh className="size-3.5" />
          </Button>
        )}

        {/* Gateway Start/Stop Button */}
        {isRunning ? (
          <Button
            variant="outline"
            size="icon-sm"
            className="border-success/30 text-success hover:bg-success/10 h-8 w-8"
            data-tour="gateway-button"
            onClick={handleGatewayToggle}
            disabled={gwLoading}
            aria-label={t("header.gateway.action.stop")}
            aria-describedby={gwError ? "gateway-status" : undefined}
          >
            <IconPower className="h-3.5 w-3.5" />
          </Button>
        ) : (
          <Button
            variant={
              isStarting || isRestarting || isStopping
                ? "secondary"
                : !canStart
                  ? "outline"
                  : "default"
            }
            size="sm"
            aria-label={
              !canStart && startReason
                ? `${t("header.gateway.action.start")}: ${startReason}`
                : t("header.gateway.action.start")
            }
            aria-describedby={gwError ? "gateway-status" : undefined}
            data-tour="gateway-button"
            className={`h-8 gap-1.5 px-2.5 text-xs font-medium ${
              isStopped
                ? "bg-primary text-primary-foreground hover:bg-primary/90"
                : ""
            } ${!canStart ? "border-warning/50 bg-warning/5 text-warning hover:bg-warning/10" : ""}`}
            onClick={handleGatewayToggle}
            disabled={gwLoading || isStarting || isRestarting || isStopping}
          >
            {gwLoading || isStarting || isRestarting || isStopping ? (
              <IconLoader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <IconPlayerPlay className="h-3.5 w-3.5" />
            )}
            <span className="hidden sm:inline">
              {transitionLabel ?? t("header.gateway.action.start")}
            </span>
          </Button>
        )}
        <Separator
          className="mx-1 hidden h-4 sm:block"
          orientation="vertical"
        />

        {/* Docs Link */}
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground hover:text-foreground hidden size-8 rounded-md sm:inline-flex"
          data-tour="docs-button"
          asChild
        >
          <a
            href={DOCS_URL}
            target="_blank"
            rel="noreferrer"
            aria-label={t("header.docs")}
          >
            <IconBook className="size-4" />
          </a>
        </Button>

        {/* Language Switcher */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground hidden size-8 rounded-md sm:inline-flex"
              aria-label={t("header.language")}
            >
              <IconLanguage className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => i18n.changeLanguage("en")}>
              English
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("zh")}>
              简体中文
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        {/* Theme Toggle */}
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground hover:text-foreground size-8 rounded-md"
          onClick={toggleTheme}
          aria-label={t(
            theme === "dark"
              ? "header.theme.switchToLight"
              : "header.theme.switchToDark",
          )}
        >
          {theme === "dark" ? (
            <IconSun className="size-4" />
          ) : (
            <IconMoon className="size-4" />
          )}
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground size-8 rounded-md sm:hidden"
              aria-label={t("header.moreActions")}
            >
              <IconDotsVertical className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            <DropdownMenuItem asChild>
              <a href={DOCS_URL} target="_blank" rel="noreferrer">
                <IconBook className="size-4" />
                {t("header.docs")}
              </a>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => i18n.changeLanguage("en")}>
              <IconLanguage className="size-4" />
              English
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("zh")}>
              <IconLanguage className="size-4" />
              简体中文
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onSelect={() => setShowLogoutDialog(true)}
            >
              <IconLogout className="size-4" />
              {t("header.logout.tooltip")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <Separator
          className="mx-1 hidden h-4 sm:block"
          orientation="vertical"
        />

        {/* Logout */}
        <div className="hidden sm:block">
          <Button
            variant="ghost"
            size="icon"
            className="text-muted-foreground hover:text-destructive size-8 rounded-md"
            onClick={() => setShowLogoutDialog(true)}
            aria-label={t("header.logout.tooltip")}
          >
            <IconLogout className="size-4" />
          </Button>
        </div>
      </div>
    </header>
  )
}
