import { IconHistory, IconTrash } from "@tabler/icons-react"
import dayjs from "dayjs"
import type { RefObject } from "react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { SessionSummary } from "@/api/sessions"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ScrollArea } from "@/components/ui/scroll-area"

interface SessionHistoryMenuProps {
  sessions: SessionSummary[]
  activeSessionId: string
  hasMore: boolean
  loadError: boolean
  loadErrorMessage: string
  isLoading: boolean
  deleteError: boolean
  observerRef: RefObject<HTMLDivElement | null>
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
}

export function SessionHistoryMenu({
  sessions,
  activeSessionId,
  hasMore,
  loadError,
  loadErrorMessage,
  isLoading,
  deleteError,
  observerRef,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const [deleteTarget, setDeleteTarget] = useState<SessionSummary | null>(null)

  return (
    <DropdownMenu onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="secondary"
          size="sm"
          className="h-9 gap-2"
          aria-label={t("chat.history")}
        >
          <IconHistory className="size-4" />
          <span className="hidden sm:inline">{t("chat.history")}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <ScrollArea className="max-h-[300px]">
          {isLoading && sessions.length === 0 && (
            <div className="space-y-2 p-2" aria-busy="true">
              <span className="sr-only">{t("chat.loadingHistory")}</span>
              {[80, 65, 72].map((width) => (
                <div
                  key={width}
                  className="bg-muted/60 h-6 animate-pulse rounded"
                  style={{ width: `${width}%` }}
                  aria-hidden="true"
                />
              ))}
            </div>
          )}
          {loadError && (
            <DropdownMenuItem disabled>
              <span className="text-destructive text-xs">
                {loadErrorMessage}
              </span>
            </DropdownMenuItem>
          )}
          {deleteError && (
            <DropdownMenuItem disabled>
              <span className="text-destructive text-xs">
                {t("chat.deleteSessionFailed")}
              </span>
            </DropdownMenuItem>
          )}
          {sessions.length === 0 && !loadError && !isLoading ? (
            <DropdownMenuItem disabled>
              <span className="text-muted-foreground text-xs">
                {t("chat.noHistory")}
              </span>
            </DropdownMenuItem>
          ) : (
            sessions.map((session) => (
              <DropdownMenuItem
                key={session.id}
                className={`group relative my-0.5 flex flex-col items-start gap-0.5 pr-8 ${
                  session.id === activeSessionId ? "bg-accent" : ""
                }`}
                onClick={() => onSwitchSession(session.id)}
              >
                <span className="line-clamp-1 text-sm font-medium">
                  {session.title}
                </span>
                <span className="text-muted-foreground text-xs">
                  {t("chat.messagesCount", {
                    count: session.message_count,
                  })}{" "}
                  · {dayjs(session.updated).fromNow()}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("chat.deleteSession")}
                  className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive absolute top-1/2 right-2 h-7 w-7 -translate-y-1/2 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 focus-visible:opacity-100"
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    setDeleteTarget(session)
                  }}
                >
                  <IconTrash className="h-4 w-4" />
                </Button>
              </DropdownMenuItem>
            ))
          )}
          {hasMore && sessions.length > 0 && (
            <div ref={observerRef} className="py-2 text-center">
              <span className="text-muted-foreground animate-pulse text-xs">
                {t("chat.loadingMore")}
              </span>
            </div>
          )}
        </ScrollArea>
      </DropdownMenuContent>
      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>{t("chat.deleteSessionTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("chat.deleteSessionDescription", {
                title: deleteTarget?.title ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) onDeleteSession(deleteTarget.id)
                setDeleteTarget(null)
              }}
            >
              {t("chat.deleteSession")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DropdownMenu>
  )
}
