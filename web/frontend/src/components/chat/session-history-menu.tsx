import {
  IconArchive,
  IconArchiveOff,
  IconChevronRight,
  IconDotsVertical,
  IconFolder,
  IconFolderPlus,
  IconLoader2,
  IconMessageCircle,
  IconPencil,
  IconPin,
  IconPlus,
  IconSearch,
  IconSettings,
  IconTrash,
} from "@tabler/icons-react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useAtomValue } from "jotai"
import { selectAtom } from "jotai/utils"
import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"

import type { ChatProject } from "@/api/projects"
import {
  searchSessions,
  type SessionSearchResult,
  type SessionSummary,
} from "@/api/sessions"
import { ProjectSettingsDialog } from "@/components/chat/project-settings-dialog"
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
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { useIsHistorySheet } from "@/hooks/use-mobile"
import {
  type SessionHistoryGroupState,
  useSessionHistory,
} from "@/hooks/use-session-history"
import { chatAtom } from "@/store/chat"

const UNCATEGORIZED_KEY = "none"
const TREE_ROW_HEIGHT = 40
const pendingSessionIdAtom = selectAtom(
  chatAtom,
  (state) => state.pendingSessionId,
)

type HistoryItem = SessionSummary | SessionSearchResult

type HistoryRow =
  | {
      kind: "project-header"
      key: string
      project?: ChatProject
      label: string
      groupKey: string
      collapsed: boolean
    }
  | {
      kind: "session"
      key: string
      session: SessionSummary
      groupKey: string
      groupIndex: number
    }
  | {
      kind: "group-status"
      key: string
      groupKey: string
      status: "loading" | "empty" | "error"
    }

interface SessionHistoryMenuProps {
  collapsed: boolean
  open: boolean
  activeSessionId: string
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string, title: string) => void
  onDeletedActiveSession: () => void
  onNewChatInProject: (projectId: string) => void
}

function itemSnippet(item: HistoryItem): string | undefined {
  return "snippet" in item ? item.snippet : undefined
}

function groupRows(
  projects: ChatProject[],
  groups: Record<string, SessionHistoryGroupState>,
  collapsedProjects: Record<string, boolean>,
  uncategorizedLabel: string,
): HistoryRow[] {
  const rows: HistoryRow[] = []
  const folders = [
    { key: UNCATEGORIZED_KEY, label: uncategorizedLabel, project: undefined },
    ...projects.map((project) => ({
      key: project.id,
      label: project.name,
      project,
    })),
  ]
  for (const folder of folders) {
    const isCollapsed = collapsedProjects[folder.key] !== false
    rows.push({
      kind: "project-header",
      key: `folder:${folder.key}`,
      project: folder.project,
      label: folder.label,
      groupKey: folder.key,
      collapsed: isCollapsed,
    })
    if (isCollapsed) continue
    const group = groups[folder.key]
    if (!group || (!group.hasLoaded && group.isLoading)) {
      rows.push({
        kind: "group-status",
        key: `status:${folder.key}:loading`,
        groupKey: folder.key,
        status: "loading",
      })
      continue
    }
    for (const [groupIndex, session] of group.items.entries()) {
      rows.push({
        kind: "session",
        key: `session:${session.id}`,
        session,
        groupKey: folder.key,
        groupIndex,
      })
    }
    if (group.loadError || group.isLoading || group.items.length === 0) {
      const status = group.loadError
        ? "error"
        : group.isLoading
          ? "loading"
          : "empty"
      rows.push({
        kind: "group-status",
        key: `status:${folder.key}`,
        groupKey: folder.key,
        status,
      })
    }
  }
  return rows
}

export const SessionHistoryMenu = memo(function SessionHistoryMenu({
  collapsed,
  open,
  activeSessionId,
  onOpenChange,
  onSwitchSession,
  onDeletedActiveSession,
  onNewChatInProject,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const pendingSessionId = useAtomValue(pendingSessionIdAtom)
  const {
    projects,
    groups,
    collapsed: collapsedProjects,
    projectsLoading,
    projectsError,
    deleteError,
    archived,
    setArchived: onArchivedChange,
    refresh: onRefresh,
    loadMore,
    retryGroup,
    toggleProject: onToggleProject,
    handleDeleteSession: onDeleteSession,
    handleUpdateSession: onUpdateSession,
    createChatProject: onCreateProject,
    updateChatProject: onUpdateProject,
    deleteChatProject: onDeleteProject,
  } = useSessionHistory({ onDeletedActiveSession })
  const [deleteTarget, setDeleteTarget] = useState<SessionSummary | null>(null)
  const [renameTarget, setRenameTarget] = useState<SessionSummary | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [projectSettings, setProjectSettings] = useState<
    ChatProject | null | undefined
  >(undefined)
  const [projectDeleteTarget, setProjectDeleteTarget] =
    useState<ChatProject | null>(null)
  const [projectDeletePending, setProjectDeletePending] = useState(false)
  const [projectDeleteError, setProjectDeleteError] = useState("")
  const [query, setQuery] = useState("")
  const [searchResults, setSearchResults] = useState<SessionSearchResult[]>([])
  const [isSearching, setIsSearching] = useState(false)
  const [searchError, setSearchError] = useState(false)
  const isHistorySheet = useIsHistorySheet()
  const [scrollElement, setScrollElement] = useState<HTMLElement | null>(null)
  const visible = isHistorySheet ? open : !collapsed
  const treePositions = useRef<
    Record<string, { offset: number; key?: string; inset: number }>
  >({})
  const searchPosition = useRef({ query: "", offset: 0 })
  const treeView = archived ? "archived" : "active"
  const normalizedQuery = query.trim()
  const rows = useMemo(
    () =>
      normalizedQuery
        ? []
        : groupRows(
            projects,
            groups,
            collapsedProjects,
            t("chat.uncategorized"),
          ),
    [collapsedProjects, groups, normalizedQuery, projects, t],
  )
  const itemCount = normalizedQuery ? searchResults.length : rows.length

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

  const getItemKey = useCallback(
    (index: number) =>
      normalizedQuery
        ? `search:${searchResults[index]?.id ?? index}`
        : (rows[index]?.key ?? index),
    [normalizedQuery, rows, searchResults],
  )
  const virtualizer = useVirtualizer({
    count: itemCount,
    getScrollElement: () => scrollElement,
    estimateSize: () => (normalizedQuery ? 64 : TREE_ROW_HEIGHT),
    getItemKey,
    overscan: 6,
    enabled: visible || (isHistorySheet && scrollElement !== null),
  })
  const virtualRows = virtualizer.getVirtualItems()

  useLayoutEffect(() => {
    const element = scrollElement
    if (!visible || !element) return
    const position = treePositions.current[treeView]
    const anchorIndex = position?.key
      ? rows.findIndex((row) => row.key === position.key)
      : -1
    const searchKey = `${treeView}:${normalizedQuery}`
    const offset = normalizedQuery
      ? searchPosition.current.query === searchKey
        ? searchPosition.current.offset
        : 0
      : position && anchorIndex >= 0
        ? anchorIndex * TREE_ROW_HEIGHT + position.inset
        : (position?.offset ?? 0)
    if (Math.abs(element.scrollTop - offset) > 1) element.scrollTop = offset
  }, [normalizedQuery, rows, scrollElement, treeView, visible])

  useEffect(() => {
    if (!visible || normalizedQuery) return
    const groupsToLoad = new Set<string>()
    for (const virtualRow of virtualRows) {
      const row = rows[virtualRow.index]
      if (row?.kind !== "session") continue
      const group = groups[row.groupKey]
      if (!group?.hasMore || group.isLoading || group.loadError) continue
      if (row.groupIndex >= group.items.length - 4)
        groupsToLoad.add(row.groupKey)
    }
    for (const key of groupsToLoad) loadMore(key)
  }, [groups, loadMore, normalizedQuery, rows, virtualRows, visible])

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

  const confirmProjectDelete = async () => {
    if (!projectDeleteTarget || projectDeletePending) return
    setProjectDeletePending(true)
    setProjectDeleteError("")
    try {
      await onDeleteProject(projectDeleteTarget.id)
      setProjectDeleteTarget(null)
    } catch (error) {
      setProjectDeleteError(
        error instanceof Error ? error.message : String(error),
      )
    } finally {
      setProjectDeletePending(false)
    }
  }

  const content = (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      <div className="flex items-center justify-between gap-1 px-2">
        <h2 className="text-muted-foreground text-xs font-medium">
          {t("chat.history")}
        </h2>
        <div className="flex items-center gap-0.5">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="text-muted-foreground hover:text-foreground size-7"
            onClick={() => setProjectSettings(null)}
            aria-label={t("chat.newProject")}
          >
            <IconFolderPlus className="size-3.5" />
          </Button>
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
        ref={setScrollElement}
        onScroll={(event) => {
          if (!visible) return
          const offset = event.currentTarget.scrollTop
          if (normalizedQuery) {
            searchPosition.current = {
              query: `${treeView}:${normalizedQuery}`,
              offset,
            }
            return
          }
          treePositions.current[treeView] = {
            offset,
            key: rows[Math.floor(offset / TREE_ROW_HEIGHT)]?.key,
            inset: offset % TREE_ROW_HEIGHT,
          }
        }}
        aria-label={t("chat.history")}
        className="min-h-0 flex-1 overflow-y-auto [overflow-anchor:none]"
      >
        {deleteError && (
          <p role="alert" className="text-destructive p-2 text-sm">
            {t("chat.deleteSessionFailed")}
          </p>
        )}
        {projectsError && (
          <div
            role="alert"
            className="text-muted-foreground flex items-center justify-between gap-2 px-2 py-2 text-xs"
          >
            <span>{t("chat.projectsLoadFailed")}</span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs"
              onClick={onRefresh}
            >
              {t("chat.historyRetry")}
            </Button>
          </div>
        )}
        {itemCount === 0 &&
          (normalizedQuery ? !isSearching && !searchError : false) && (
            <p className="text-muted-foreground p-2 text-sm">
              {t("chat.noMatchingHistory")}
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
          {virtualRows.map((virtualRow) => {
            if (normalizedQuery) {
              const session = searchResults[virtualRow.index]
              if (!session) return null
              const snippet = itemSnippet(session)
              return (
                <div
                  key={`search:${session.id}`}
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    width: "100%",
                    height: 64,
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                  className="group hover:bg-muted flex items-center rounded-md transition-colors"
                >
                  <button
                    type="button"
                    aria-busy={pendingSessionId === session.id || undefined}
                    aria-current={
                      activeSessionId === session.id ? "page" : undefined
                    }
                    className="focus-visible:ring-ring flex min-h-16 min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-2 text-left focus-visible:ring-2 focus-visible:outline-none"
                    onClick={() => {
                      onSwitchSession(session.id, session.title)
                      onOpenChange(false)
                    }}
                  >
                    <IconMessageCircle className="text-muted-foreground size-4 shrink-0" />
                    <span className="block min-w-0 flex-1">
                      <span className="block truncate text-sm">
                        {session.title}
                      </span>
                      <span className="text-muted-foreground block truncate text-[11px] leading-4">
                        {[session.project_name, snippet]
                          .filter(Boolean)
                          .join(" · ") || t("chat.uncategorized")}
                      </span>
                    </span>
                  </button>
                </div>
              )
            }

            const row = rows[virtualRow.index]
            if (!row) return null
            const positionStyle = {
              position: "absolute" as const,
              top: 0,
              left: 0,
              width: "100%",
              height: TREE_ROW_HEIGHT,
              transform: `translateY(${virtualRow.start}px)`,
            }
            if (row.kind === "project-header") {
              const expanded = !row.collapsed
              return (
                <div
                  key={row.key}
                  style={positionStyle}
                  className="group hover:bg-muted flex items-center rounded-md"
                >
                  <button
                    type="button"
                    className="focus-visible:ring-ring flex min-h-10 min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 text-left focus-visible:ring-2 focus-visible:outline-none"
                    aria-expanded={expanded}
                    onClick={() => onToggleProject(row.groupKey)}
                  >
                    <IconChevronRight
                      className={`text-muted-foreground size-3.5 shrink-0 transition-transform duration-[var(--motion-state)] motion-reduce:rotate-0 motion-reduce:transition-none ${expanded ? "rotate-90" : ""}`}
                    />
                    {row.project ? (
                      <IconFolder className="text-muted-foreground size-4 shrink-0" />
                    ) : null}
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">
                      {row.label}
                    </span>
                  </button>
                  {row.project && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-muted-foreground size-8 shrink-0 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-focus-within:opacity-100 [@media(hover:hover)]:group-hover:opacity-100"
                          aria-label={t("chat.projectActions")}
                        >
                          <IconDotsVertical className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="min-w-44">
                        <DropdownMenuItem
                          onSelect={() => onNewChatInProject(row.project!.id)}
                        >
                          <IconPlus />
                          {t("chat.newChatInProject")}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onSelect={() => setProjectSettings(row.project!)}
                        >
                          <IconSettings />
                          {t("chat.projectSettings")}
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          onSelect={() => {
                            setProjectDeleteError("")
                            setProjectDeleteTarget(row.project!)
                          }}
                        >
                          <IconTrash />
                          {t("chat.deleteProject")}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                </div>
              )
            }
            if (row.kind === "group-status") {
              return (
                <div
                  key={row.key}
                  style={positionStyle}
                  className="text-muted-foreground flex items-center px-2 pl-9 text-xs"
                  role={row.status === "error" ? "alert" : undefined}
                >
                  {row.status === "loading" && t("chat.loadingHistory")}
                  {row.status === "empty" && t("chat.noHistory")}
                  {row.status === "error" && (
                    <span className="flex min-w-0 flex-1 items-center justify-between gap-2">
                      <span>{t("chat.historyLoadFailed")}</span>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 px-2 text-xs"
                        onClick={() => retryGroup(row.groupKey)}
                      >
                        {t("chat.historyRetry")}
                      </Button>
                    </span>
                  )}
                </div>
              )
            }

            const session = row.session
            const isOpening = pendingSessionId === session.id
            return (
              <div
                key={row.key}
                style={positionStyle}
                className={`group hover:bg-muted flex items-center rounded-md transition-colors ${session.id === activeSessionId ? "bg-muted font-medium" : ""}`}
              >
                <button
                  type="button"
                  aria-busy={isOpening || undefined}
                  aria-current={
                    session.id === activeSessionId ? "page" : undefined
                  }
                  className="focus-visible:ring-ring flex min-h-10 min-w-0 flex-1 items-center gap-2 rounded-md py-2 pr-2 pl-9 text-left focus-visible:ring-2 focus-visible:outline-none"
                  onClick={() => {
                    onSwitchSession(session.id, session.title)
                    onOpenChange(false)
                  }}
                >
                  {isOpening ? (
                    <IconLoader2 className="text-muted-foreground size-3.5 shrink-0 animate-spin motion-reduce:animate-none" />
                  ) : session.pinned ? (
                    <IconPin className="text-accent size-3.5 shrink-0" />
                  ) : (
                    <IconMessageCircle className="text-muted-foreground size-4 shrink-0" />
                  )}
                  <span className="block min-w-0 flex-1 truncate text-sm">
                    {session.title}
                  </span>
                </button>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-muted-foreground size-8 shrink-0 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-focus-within:opacity-100 [@media(hover:hover)]:group-hover:opacity-100"
                      aria-label={t("chat.sessionActions")}
                    >
                      <IconDotsVertical className="size-4" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="min-w-44">
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
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <IconFolder />
                        {t("chat.moveToProject")}
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="min-w-44">
                        <DropdownMenuItem
                          disabled={!session.project_id}
                          onSelect={() =>
                            void onUpdateSession(session.id, {
                              project_id: null,
                            })
                          }
                        >
                          {t("chat.uncategorized")}
                        </DropdownMenuItem>
                        {projects.map((project) => (
                          <DropdownMenuItem
                            key={project.id}
                            disabled={session.project_id === project.id}
                            onSelect={() =>
                              void onUpdateSession(session.id, {
                                project_id: project.id,
                              })
                            }
                          >
                            <IconFolder />
                            <span className="truncate">{project.name}</span>
                          </DropdownMenuItem>
                        ))}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                    <DropdownMenuItem
                      onSelect={() =>
                        void onUpdateSession(session.id, {
                          archived: !session.archived,
                        })
                      }
                    >
                      {session.archived ? <IconArchiveOff /> : <IconArchive />}
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
              </div>
            )
          })}
        </div>
        {isSearching && (
          <p role="status" className="text-muted-foreground px-2 py-3 text-xs">
            {t("chat.searchingHistory")}
          </p>
        )}
        {projectsLoading && projects.length === 0 && (
          <p role="status" className="text-muted-foreground px-2 py-2 text-xs">
            {t("chat.loadingProjects")}
          </p>
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

      <ProjectSettingsDialog
        open={projectSettings !== undefined}
        project={projectSettings ?? null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setProjectSettings(undefined)
        }}
        onCreate={onCreateProject}
        onUpdate={onUpdateProject}
        onDelete={onDeleteProject}
      />

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

      <AlertDialog
        open={projectDeleteTarget !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !projectDeletePending) {
            setProjectDeleteTarget(null)
            setProjectDeleteError("")
          }
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>{t("chat.deleteProjectTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("chat.deleteProjectDescription", {
                name: projectDeleteTarget?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {projectDeleteError && (
            <p role="alert" className="text-destructive text-sm">
              {projectDeleteError}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={projectDeletePending}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={projectDeletePending}
              onClick={(event) => {
                event.preventDefault()
                void confirmProjectDelete()
              }}
            >
              {projectDeletePending
                ? t("chat.deletingProject")
                : t("chat.deleteProject")}
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
})
