import { IconMessageCircle, IconSearch, IconTrash } from "@tabler/icons-react"
import dayjs from "dayjs"
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
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"

interface SessionHistoryMenuProps {
  collapsed: boolean
  open: boolean
  onRefresh: () => void
  onLoadMore: () => void
  sessions: SessionSummary[]
  activeSessionId: string
  hasMore: boolean
  loadError: boolean
  loadErrorMessage: string
  isLoading: boolean
  deleteError: boolean
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
}

export function SessionHistoryMenu({
  collapsed,
  open,
  onRefresh,
  onLoadMore,
  sessions,
  activeSessionId,
  hasMore,
  loadError,
  loadErrorMessage,
  isLoading,
  deleteError,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const [deleteTarget, setDeleteTarget] = useState<SessionSummary | null>(null)

  const [query, setQuery] = useState("")
  const filtered = sessions.filter((session) =>
    `${session.title} ${session.preview}`
      .toLocaleLowerCase()
      .includes(query.toLocaleLowerCase()),
  )
  const content = (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      <h2 className="text-muted-foreground px-2 text-xs font-medium">
        {t("chat.history")}
      </h2>
      <label className="bg-card border-input focus-within:ring-ring/40 flex items-center gap-2 rounded-md border px-3 py-2 focus-within:ring-2">
        <IconSearch className="text-muted-foreground size-4 shrink-0" />
        <input
          className="w-full min-w-0 bg-transparent text-sm outline-none"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("chat.filterHistory")}
          aria-label={t("chat.filterHistory")}
        />
      </label>
      <nav
        aria-label={t("chat.history")}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {isLoading && (
          <p role="status" className="text-muted-foreground p-2 text-sm">
            {t("chat.loadingHistory")}
          </p>
        )}
        {loadError && (
          <div role="alert" className="p-2 text-sm">
            <p>{loadErrorMessage}</p>
            <Button variant="ghost" size="sm" onClick={onRefresh}>
              {t("chat.historyRetry")}
            </Button>
          </div>
        )}
        {deleteError && (
          <p role="alert" className="text-destructive p-2 text-sm">
            {t("chat.deleteSessionFailed")}
          </p>
        )}
        {!isLoading && !loadError && filtered.length === 0 && (
          <p className="text-muted-foreground p-2 text-sm">
            {t(query ? "chat.noMatchingHistory" : "chat.noHistory")}
          </p>
        )}
        {filtered.map((session) => (
          <div
            key={session.id}
            className={`group hover:bg-muted flex items-center rounded-md transition-colors ${session.id === activeSessionId ? "bg-muted font-medium" : ""}`}
          >
            <button
              type="button"
              aria-current={session.id === activeSessionId ? "page" : undefined}
              className="focus-visible:ring-ring flex min-h-10 min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-2 text-left focus-visible:ring-2 focus-visible:outline-none"
              title={session.title}
              onClick={() => {
                onSwitchSession(session.id)
                onOpenChange(false)
              }}
            >
              <IconMessageCircle className="text-muted-foreground size-4 shrink-0" />
              <span className="block min-w-0 flex-1 truncate text-sm">
                {session.title}
              </span>
              <span className="text-muted-foreground shrink-0 text-[11px] font-normal">
                {dayjs(session.updated).format("MM-DD")}
              </span>
            </button>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground mr-1 size-8 shrink-0 md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100"
              aria-label={t("chat.deleteSession")}
              onClick={() => setDeleteTarget(session)}
            >
              <IconTrash className="size-4" />
            </Button>
          </div>
        ))}
        {hasMore && !isLoading && (
          <Button
            variant="ghost"
            className="mt-2 w-full text-sm"
            onClick={onLoadMore}
          >
            {t("chat.loadMoreSessions")}
          </Button>
        )}
      </nav>
    </div>
  )
  return (
    <>
      <aside
        className={`bg-background border-border hidden h-full w-60 shrink-0 border-r ${collapsed ? "" : "md:block"}`}
      >
        {content}
      </aside>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="left"
          aria-describedby={undefined}
          className="bg-background gap-0 p-0 pt-4"
        >
          <SheetTitle className="sr-only">{t("chat.history")}</SheetTitle>
          {content}
        </SheetContent>
      </Sheet>
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
    </>
  )
}
