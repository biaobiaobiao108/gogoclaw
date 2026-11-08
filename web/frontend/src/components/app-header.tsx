import {
  IconBook,
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
import { Link } from "@tanstack/react-router"
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
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu.tsx"
import { Separator } from "@/components/ui/separator.tsx"
import { SidebarTrigger } from "@/components/ui/sidebar"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useGateway } from "@/hooks/use-gateway.ts"
import { useTheme } from "@/hooks/use-theme.ts"

export function AppHeader() {
  const { i18n, t } = useTranslation()
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
  const showNotConnectedHint =
    !isRestarting &&
    !isStopping &&
    canStart &&
    (gwState === "stopped" || gwState === "error")

  const [showStopDialog, setShowStopDialog] = React.useState(false)
  const [showLogoutDialog, setShowLogoutDialog] = React.useState(false)

  const handleLogout = async () => {
    await postLauncherDashboardLogout()
    globalThis.location.assign("/launcher-login")
  }

  const handleGatewayToggle = () => {
    if (gwLoading || isRestarting || isStopping || (!isRunning && !canStart)) {
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
    <header className="bg-background/80 supports-backdrop-filter:bg-background/60 border-border/40 sticky top-0 z-50 flex h-11 shrink-0 items-center justify-between border-b px-3 backdrop-blur-md">
      <div className="flex items-center gap-3">
        <SidebarTrigger className="text-muted-foreground hover:bg-muted hover:text-foreground flex h-8 w-8 items-center justify-center rounded-md sm:hidden [&>svg]:size-4">
          <IconMenu2 />
        </SidebarTrigger>
        <div className="hidden w-28 shrink-0 items-center sm:flex">
          <Link
            to="/"
            className="opacity-90 transition-opacity hover:opacity-100"
          >
            <img className="h-5 w-auto" src="/logo_with_text.png" alt="Logo" />
          </Link>
        </div>
      </div>

      {/* Precision status indicator */}
      <div className="pointer-events-none absolute left-1/2 hidden h-full -translate-x-1/2 items-center justify-center lg:flex">
        {showNotConnectedHint && (
          <div className="bg-card/80 text-muted-foreground border-border/80 flex items-center gap-2 rounded-full border px-3 py-1 text-xs shadow-xs">
            <span className="relative flex size-2 shrink-0 items-center justify-center">
              <span className="bg-destructive absolute inline-flex size-full animate-ping rounded-full opacity-75"></span>
              <span className="bg-destructive relative inline-flex size-1.5 rounded-full"></span>
            </span>
            <span className="font-mono text-[11px] tracking-tight">
              {t("chat.notConnected")}
            </span>
          </div>
        )}
        {isRunning && (
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <span className="relative flex size-2 shrink-0 items-center justify-center">
              <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-60"></span>
              <span className="relative inline-flex size-1.5 rounded-full bg-emerald-500"></span>
            </span>
            <span className="text-foreground/80 font-mono text-[11px] font-medium tracking-tight">
              ONLINE
            </span>
          </div>
        )}
      </div>

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

      <div className="text-muted-foreground flex items-center gap-1 text-sm md:gap-1.5">
        {restartRequired && (
          <Tooltip delayDuration={300}>
            <TooltipTrigger asChild>
              <Button
                variant="secondary"
                size="icon-sm"
                className="h-8 w-8 bg-amber-500/10 text-amber-600 hover:bg-amber-500/20 dark:text-amber-400"
                onClick={handleGatewayRestart}
                disabled={gwLoading || isRestarting || isStopping || !canStart}
                aria-label={t("header.gateway.action.restart")}
              >
                <IconRefresh className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {t("header.gateway.restartRequired")}
            </TooltipContent>
          </Tooltip>
        )}

        {/* Gateway Start/Stop Button */}
        {isRunning ? (
          <Tooltip delayDuration={300}>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="icon-sm"
                className="h-8 w-8 border-emerald-500/30 text-emerald-600 hover:bg-emerald-500/10 dark:text-emerald-400"
                data-tour="gateway-button"
                onClick={handleGatewayToggle}
                disabled={gwLoading}
                aria-label={t("header.gateway.action.stop")}
              >
                <IconPower className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {gwError ?? t("header.gateway.action.stop")}
            </TooltipContent>
          </Tooltip>
        ) : (
          <Tooltip
            delayDuration={gwError || (!canStart && startReason) ? 0 : 300}
          >
            <TooltipTrigger asChild>
              <span
                className={
                  !canStart && startReason ? "cursor-not-allowed" : undefined
                }
                tabIndex={!canStart && startReason ? 0 : undefined}
              >
                <Button
                  variant={
                    isStarting || isRestarting || isStopping
                      ? "secondary"
                      : "default"
                  }
                  size="sm"
                  data-tour="gateway-button"
                  className={`h-8 gap-1.5 px-2.5 text-xs font-medium ${
                    isStopped
                      ? "bg-primary text-primary-foreground hover:bg-primary/90"
                      : ""
                  } ${!canStart ? "pointer-events-none opacity-50" : ""}`}
                  onClick={handleGatewayToggle}
                  disabled={
                    gwLoading ||
                    isStarting ||
                    isRestarting ||
                    isStopping ||
                    !canStart
                  }
                >
                  {gwLoading || isStarting || isRestarting || isStopping ? (
                    <IconLoader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <IconPlayerPlay className="h-3.5 w-3.5" />
                  )}
                  <span>
                    {isStopping
                      ? t("header.gateway.status.stopping")
                      : isRestarting
                        ? t("header.gateway.status.restarting")
                        : isStarting
                          ? t("header.gateway.status.starting")
                          : t("header.gateway.action.start")}
                  </span>
                </Button>
              </span>
            </TooltipTrigger>
            {gwError || (!canStart && startReason) ? (
              <TooltipContent>{gwError ?? startReason}</TooltipContent>
            ) : null}
          </Tooltip>
        )}

        <Separator className="mx-1 h-4" orientation="vertical" />

        {/* Docs Link */}
        <Button
          variant="ghost"
          size="icon"
          className="text-muted-foreground hover:text-foreground size-8 rounded-md"
          data-tour="docs-button"
          asChild
        >
          <a href="https://docs.picoclaw.io" target="_blank" rel="noreferrer">
            <IconBook className="size-4" />
          </a>
        </Button>

        {/* Language Switcher */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground size-8 rounded-md"
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
        >
          {theme === "dark" ? (
            <IconSun className="size-4" />
          ) : (
            <IconMoon className="size-4" />
          )}
        </Button>

        <Separator className="mx-1 h-4" orientation="vertical" />

        {/* Logout */}
        <Tooltip delayDuration={300}>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-destructive size-8 rounded-md"
              onClick={() => setShowLogoutDialog(true)}
              aria-label={t("header.logout.tooltip")}
            >
              <IconLogout className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t("header.logout.tooltip")}</TooltipContent>
        </Tooltip>
      </div>
    </header>
  )
}
