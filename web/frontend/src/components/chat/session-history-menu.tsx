import {
  IconArchive,
  IconArchiveOff,
  IconDotsVertical,
  IconMessageCircle,
  IconPencil,
  IconPin,
  IconSearch,
  IconTrash,
} from "@tabler/icons-react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  searchSessions,
  type SessionSearchResult,
  type SessionSummary,
} from "@/api/sessions"
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
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { useIsHistorySheet } from "@/hooks/use-mobile"

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
  archived: boolean
  onArchivedChange: (archived: boolean) => void
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
  onUpdateSession: (
    sessionId: string,
    update: { title?: string; pinned?: boolean; archived?: boolean },
  ) => Promise<void>
}

type HistoryItem = SessionSummary | SessionSearchResult

function itemSnippet(item: HistoryItem): string | undefined {
  return "snippet" in item ? item.snippet : undefined
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
  archived,
  onArchivedChange,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
  onUpdateSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const [deleteTarget, setDeleteTarget] = useState<SessionSummary | null>(null)
  const [renameTarget, setRenameTarget] = useState<SessionSummary | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [query, setQuery] = useState("")
  const [searchResults, setSearchResults] = useState<SessionSearchResult[]>([])
  const [isSearching, setIsSearching] = useState(false)
  const [searchError, setSearchError] = useState(false)
  const isHistorySheet = useIsHistorySheet()
  const scrollRef = useRef<HTMLElement>(null)
  const visible = isHistorySheet ? open : !collapsed
  const normalizedQuery = query.trim()
  const items: HistoryItem[] = normalizedQuery ? searchResults : sessions

  useEffect(() => {
    if (!normalizedQuery) {
      setSearchResults([])
      setIsSearching(false)
      setSearchError(false)
      return
    }
    let cancelled = false
    const controller = new AbortController()
    setIsSearching(true)
    setSearchResults([])
    setSearchError(false)
    const timer = window.setTimeout(() => {
      void searchSessions(normalizedQuery, archived, 30, controller.signal)
        .then((results) => {
          if (!cancelled) setSearchResults(results)
        })
        .catch((error) => {
          if (error instanceof DOMException && error.name === "AbortError")
            return
          console.error("Failed to search chat history:", error)
          if (!cancelled) setSearchError(true)
        })
        .finally(() => {
          if (!cancelled) setIsSearching(false)
        })
    }, 220)
    return () => {
      cancelled = true
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [archived, normalizedQuery])

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => (normalizedQuery ? 64 : 40),
    getItemKey: (index) => items[index].id,
    overscan: 6,
    enabled: visible,
  })
  const virtualRows = virtualizer.getVirtualItems()
  const lastIndex = virtualRows.at(-1)?.index ?? -1
  useEffect(() => {
    if (
      visible &&
      !normalizedQuery &&
      hasMore &&
      !isLoading &&
      !loadError &&
      lastIndex >= items.length - 4 &&
      items.length > 0
    )
      onLoadMore()
  }, [
    visible,
    normalizedQuery,
    hasMore,
    isLoading,
    loadError,
    lastIndex,
    items.length,
    onLoadMore,
  ])
  useEffect(() => {
    virtualizer.scrollToOffset(0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [normalizedQuery, archived])

  const openRename = (session: SessionSummary) => {
    setRenameTarget(session)
    setRenameValue(session.title)
  }

  const saveRename = () => {
    if (!renameTarget) return
    const title = renameValue.trim()
    if (!title) return
    void onUpdateSession(renameTarget.id, { title })
    setRenameTarget(null)
  }

  const content = (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      <div className="flex items-center justify-between gap-2 px-2">
        <h2 className="text-muted-foreground text-xs font-medium">
          {t("chat.history")}
        </h2>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-foreground size-7"
          onClick={() => {
            setQuery("")
            onArchivedChange(!archived)
          }}
          aria-label={
            archived ? t("chat.showActiveChats") : t("chat.showArchivedChats")
          }
        >
          {archived ? (
            <IconArchiveOff className="size-3.5" />
          ) : (
            <IconArchive className="size-3.5" />
          )}
        </Button>
      </div>
      <label className="bg-card border-input focus-within:ring-ring/40 flex items-center gap-2 rounded-md border px-3 py-2 focus-within:ring-2">
        <IconSearch className="text-muted-foreground size-4 shrink-0" />
        <input
          className="placeholder:text-muted-foreground w-full min-w-0 bg-transparent text-sm outline-none"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("chat.searchAllHistory")}
          aria-label={t("chat.searchAllHistory")}
        />
      </label>
      <nav
        ref={scrollRef}
        aria-label={t("chat.history")}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {deleteError && (
          <p role="alert" className="text-destructive p-2 text-sm">
            {t("chat.deleteSessionFailed")}
          </p>
        )}
        {!isLoading &&
          !loadError &&
          !isSearching &&
          !searchError &&
          items.length === 0 && (
            <p className="text-muted-foreground p-2 text-sm">
              {t(normalizedQuery ? "chat.noMatchingHistory" : "chat.noHistory")}
            </p>
          )}
        {searchError && (
          <p role="alert" className="text-destructive p-2 text-sm">
            {t("chat.searchFailed")}
          </p>
        )}
        <div
          className="relative w-full"
          style={{ height: virtualizer.getTotalSize() }}
        >
          {virtualRows.map((row) => {
            const session = items[row.index]
            const snippet = itemSnippet(session)
            const rowHeight = normalizedQuery ? 64 : 40
            return (
              <div
                key={session.id}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: rowHeight,
                  transform: `translateY(${row.start}px)`,
                }}
                className={`group hover:bg-muted flex items-center rounded-md transition-colors ${session.id === activeSessionId ? "bg-muted font-medium" : ""}`}
              >
                <button
                  type="button"
                  aria-current={
                    session.id === activeSessionId ? "page" : undefined
                  }
                  className="focus-visible:ring-ring flex min-h-10 min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-2 text-left focus-visible:ring-2 focus-visible:outline-none"
                  onClick={() => {
                    onSwitchSession(session.id)
                    if (normalizedQuery) setQuery("")
                    onOpenChange(false)
                  }}
                >
                  {session.pinned ? (
                    <IconPin className="text-accent size-3.5 shrink-0" />
                  ) : (
                    <IconMessageCircle className="text-muted-foreground size-4 shrink-0" />
                  )}
                  <span className="block min-w-0 flex-1 truncate text-sm">
                    {session.title}
                    {snippet && (
                      <span className="text-muted-foreground block truncate text-[11px] leading-4">
                        {snippet}
                      </span>
                    )}
                  </span>
                </button>
                {!normalizedQuery && (
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground size-8 shrink-0 md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100"
                        aria-label={t("chat.sessionActions")}
                      >
                        <IconDotsVertical className="size-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="min-w-40">
                      <DropdownMenuItem onSelect={() => openRename(session)}>
                        <IconPencil />
                        {t("chat.renameSession")}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          void onUpdateSession(session.id, {
                            pinned: !session.pinned,
                          })
                        }
                      >
                        <IconPin />
                        {session.pinned
                          ? t("chat.unpinSession")
                          : t("chat.pinSession")}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          void onUpdateSession(session.id, {
                            archived: !session.archived,
                          })
                        }
                      >
                        {session.archived ? (
                          <IconArchiveOff />
                        ) : (
                          <IconArchive />
                        )}
                        {session.archived
                          ? t("chat.unarchiveSession")
                          : t("chat.archiveSession")}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onSelect={() => setDeleteTarget(session)}
                      >
                        <IconTrash />
                        {t("chat.deleteSession")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                )}
              </div>
            )
          })}
        </div>
        {(isLoading || isSearching) && (
          <p role="status" className="text-muted-foreground px-2 py-3 text-xs">
            {isSearching
              ? t("chat.searchingHistory")
              : t(sessions.length ? "chat.loadingMore" : "chat.loadingHistory")}
          </p>
        )}
        {loadError && (
          <div
            role="alert"
            className="text-muted-foreground flex flex-wrap items-center justify-between gap-2 px-2 py-3 text-xs"
          >
            <span>{loadErrorMessage}</span>
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={isLoading}
              onClick={sessions.length ? onLoadMore : onRefresh}
            >
              {t("chat.historyRetry")}
            </Button>
          </div>
        )}
      </nav>
    </div>
  )

  return (
    <>
      {!isHistorySheet && (
        <aside
          className={`bg-background border-r-border/20 hidden h-full w-60 shrink-0 border-r ${collapsed ? "" : "md:block"}`}
        >
          {content}
        </aside>
      )}
      <Sheet open={isHistorySheet && open} onOpenChange={onOpenChange}>
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
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setDeleteTarget(null)
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
      <Dialog
        open={renameTarget !== null}
        onOpenChange={(nextOpen) => !nextOpen && setRenameTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("chat.renameSession")}</DialogTitle>
          </DialogHeader>
          <Input
            autoFocus
            value={renameValue}
            onChange={(event) => setRenameValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return
              if (event.key === "Enter") saveRename()
            }}
            aria-label={t("chat.renameSession")}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={saveRename} disabled={!renameValue.trim()}>
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
