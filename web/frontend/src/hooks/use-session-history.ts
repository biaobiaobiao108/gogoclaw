import { useAtomValue } from "jotai"
import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  type ChatProject,
  createProject,
  deleteProject,
  getProjects,
  updateProject,
} from "@/api/projects"
import {
  deleteSession,
  getSessions,
  type SessionSummary,
  updateSessionMetadata,
} from "@/api/sessions"
import {
  activeChatProjectIdAtom,
  getChatState,
  updateChatStore,
} from "@/store/chat"

const LIMIT = 20
const UNCATEGORIZED_KEY = "none"
const COLLAPSED_STORAGE_KEY = "gogoclaw.chat.projectTree.collapsed.v1"

export interface SessionHistoryGroupState {
  key: string
  items: SessionSummary[]
  cursor?: string
  hasMore: boolean
  hasLoaded: boolean
  isLoading: boolean
  loadError: boolean
}

interface UseSessionHistoryOptions {
  onDeletedActiveSession: () => void
}

function emptyGroup(key: string): SessionHistoryGroupState {
  return {
    key,
    items: [],
    hasMore: true,
    hasLoaded: false,
    isLoading: false,
    loadError: false,
  }
}

function groupKey(projectID?: string | null) {
  return projectID || UNCATEGORIZED_KEY
}

function readCollapsedState(): Record<string, boolean> {
  try {
    const raw = window.localStorage.getItem(COLLAPSED_STORAGE_KEY)
    if (!raw) return { [UNCATEGORIZED_KEY]: false }
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === "object"
      ? { [UNCATEGORIZED_KEY]: false, ...parsed }
      : { [UNCATEGORIZED_KEY]: false }
  } catch {
    return { [UNCATEGORIZED_KEY]: false }
  }
}

function writeCollapsedState(value: Record<string, boolean>) {
  try {
    window.localStorage.setItem(COLLAPSED_STORAGE_KEY, JSON.stringify(value))
  } catch {
    // Persistence is a convenience; the tree remains usable without storage.
  }
}

function sortProjects(projects: ChatProject[]) {
  return [...projects].sort((left, right) =>
    left.name.localeCompare(right.name, undefined, { sensitivity: "base" }),
  )
}

type View = "active" | "archived"
type Groups = Record<string, SessionHistoryGroupState>
type LoadMode = "ensure" | "more" | "refresh"

function retainItems(previous: SessionSummary[], next: SessionSummary[]) {
  const byID = new Map(previous.map((item) => [item.id, item]))
  const items = next.map((item) => {
    const old = byID.get(item.id)
    return old &&
      Object.keys(item).length === Object.keys(old).length &&
      Object.entries(item).every(
        ([key, value]) => old[key as keyof SessionSummary] === value,
      )
      ? old
      : item
  })
  return items.length === previous.length &&
    items.every((item, index) => item === previous[index])
    ? previous
    : items
}

export function useSessionHistory({
  onDeletedActiveSession,
}: UseSessionHistoryOptions) {
  const { t } = useTranslation()
  const activeProjectId = useAtomValue(activeChatProjectIdAtom)
  const [view, setView] = useState<View>("active")
  const viewRef = useRef<View>("active")
  const [views, setViews] = useState<Record<View, Groups>>({
    active: {},
    archived: {},
  })
  const viewsRef = useRef(views)
  const [projects, setProjects] = useState<ChatProject[]>([])
  const projectsRef = useRef(projects)
  const [projectsLoading, setProjectsLoading] = useState(true)
  const [projectsError, setProjectsError] = useState(false)
  const projectsRequest = useRef<Promise<void> | null>(null)
  const projectsRevision = useRef(0)
  const [collapsed, setCollapsedState] = useState(readCollapsedState)
  const collapsedRef = useRef(collapsed)
  const [deleteError, setDeleteError] = useState(false)
  const requests = useRef(new Map<string, AbortController>())

  const setGroups = useCallback(
    (scope: View, update: (previous: Groups) => Groups) => {
      const previous = viewsRef.current
      const groups = update(previous[scope])
      if (groups === previous[scope]) return
      const next = { ...previous, [scope]: groups }
      viewsRef.current = next
      setViews(next)
    },
    [],
  )

  const setProjectsValue = useCallback((next: ChatProject[]) => {
    projectsRevision.current++
    projectsRef.current = next
    setProjects(next)
  }, [])

  const setCollapsed = useCallback((key: string, isCollapsed: boolean) => {
    if (collapsedRef.current[key] === isCollapsed) return
    const next = { ...collapsedRef.current, [key]: isCollapsed }
    collapsedRef.current = next
    setCollapsedState(next)
    writeCollapsedState(next)
  }, [])

  const refreshProjects = useCallback(
    (force = false) => {
      if (!force && projectsRequest.current) return projectsRequest.current
      const revision = projectsRevision.current
      const request = getProjects()
        .then((next) => {
          if (
            projectsRequest.current !== request ||
            projectsRevision.current !== revision
          )
            return
          setProjectsValue(sortProjects(next))
          setProjectsError(false)
        })
        .catch((error) => {
          if (projectsRequest.current !== request) return
          console.error("Failed to load chat projects:", error)
          setProjectsError(true)
        })
        .finally(() => {
          if (projectsRequest.current !== request) return
          projectsRequest.current = null
          setProjectsLoading(false)
        })
      projectsRequest.current = request
      return request
    },
    [setProjectsValue],
  )

  const loadGroup = useCallback(
    async (key: string, mode: LoadMode, scope = viewRef.current) => {
      const ticket = `${scope}:${key}`
      const pending = requests.current.get(ticket)
      const current = viewsRef.current[scope][key] ?? emptyGroup(key)
      if (mode === "ensure" && (current.hasLoaded || pending)) return
      if (
        mode === "more" &&
        (pending || (current.hasLoaded && !current.hasMore))
      )
        return
      pending?.abort()
      const request = new AbortController()
      requests.current.set(ticket, request)
      // Refreshes keep existing rows and pagination visible until a replacement is ready.
      const showLoading = mode !== "refresh" || !current.hasLoaded
      if (
        showLoading !== current.isLoading ||
        current.loadError ||
        !current.hasLoaded
      ) {
        setGroups(scope, (previous) => ({
          ...previous,
          [key]: { ...current, isLoading: showLoading, loadError: false },
        }))
      }
      try {
        const depth =
          mode === "refresh" ? Math.max(LIMIT, current.items.length) : LIMIT
        let cursor = mode === "more" ? current.cursor : undefined
        const items = new Map(
          (mode === "more" ? current.items : []).map((item) => [item.id, item]),
        )
        let hasMore: boolean
        do {
          const page = await getSessions(
            cursor,
            Math.min(100, mode === "refresh" ? depth - items.size : LIMIT),
            scope === "archived",
            key,
            request.signal,
          )
          if (requests.current.get(ticket) !== request) return
          for (const item of page.items) items.set(item.id, item)
          hasMore = Boolean(
            page.has_more && page.next_cursor && page.next_cursor !== cursor,
          )
          cursor = page.next_cursor
        } while (mode === "refresh" && hasMore && items.size < depth)
        const chat = getChatState()
        const activeTitle = items.get(chat.activeSessionId)?.title
        if (activeTitle && activeTitle !== chat.activeSessionTitle) {
          updateChatStore({ activeSessionTitle: activeTitle })
        }
        setGroups(scope, (previous) => {
          const group = previous[key]
          const next = retainItems(group?.items ?? [], [...items.values()])
          if (
            group?.hasLoaded &&
            !group.isLoading &&
            !group.loadError &&
            next === group.items &&
            cursor === group.cursor &&
            hasMore === group.hasMore
          )
            return previous
          return {
            ...previous,
            [key]: {
              key,
              items: next,
              cursor,
              hasMore,
              hasLoaded: true,
              isLoading: false,
              loadError: false,
            },
          }
        })
      } catch (error) {
        if (requests.current.get(ticket) !== request || request.signal.aborted)
          return
        console.error("Failed to fetch chat group:", error)
        setGroups(scope, (previous) => ({
          ...previous,
          [key]: { ...previous[key], isLoading: false, loadError: true },
        }))
      } finally {
        if (requests.current.get(ticket) === request)
          requests.current.delete(ticket)
      }
    },
    [setGroups],
  )

  const loadVisibleGroups = useCallback(
    (scope: View, mode: LoadMode) => {
      const keys = [
        UNCATEGORIZED_KEY,
        ...projectsRef.current.map(({ id }) => id),
      ].filter((key) => collapsedRef.current[key] === false)
      return Promise.all(keys.map((key) => loadGroup(key, mode, scope)))
    },
    [loadGroup],
  )

  useEffect(() => {
    void loadVisibleGroups(viewRef.current, "ensure")
    let mounted = true
    void refreshProjects().then(() => {
      if (mounted) void loadVisibleGroups(viewRef.current, "ensure")
    })
    const controllerMap = requests.current
    return () => {
      mounted = false
      for (const request of controllerMap.values()) request.abort()
      controllerMap.clear()
    }
  }, [loadVisibleGroups, refreshProjects])

  useEffect(() => {
    if (activeProjectId) {
      setCollapsed(activeProjectId, false)
      void loadGroup(activeProjectId, "ensure")
    }
  }, [activeProjectId, loadGroup, setCollapsed])

  useEffect(() => {
    const timers = new Map<string, ReturnType<typeof setTimeout>>()
    const refreshActiveGroup = () => {
      const chat = getChatState()
      const key = groupKey(chat.activeProjectId)
      const archived = viewsRef.current.archived[key]?.items.some(
        (item) => item.id === chat.activeSessionId,
      )
      const scope = archived ? "archived" : "active"
      const ticket = `${scope}:${key}`
      const timer = timers.get(ticket)
      if (timer !== undefined) clearTimeout(timer)
      timers.set(
        ticket,
        setTimeout(() => {
          timers.delete(ticket)
          void loadGroup(key, "refresh", scope)
        }, 150),
      )
    }
    window.addEventListener("chat-session-list-updated", refreshActiveGroup)
    return () => {
      window.removeEventListener(
        "chat-session-list-updated",
        refreshActiveGroup,
      )
      for (const timer of timers.values()) clearTimeout(timer)
    }
  }, [loadGroup])

  const refresh = useCallback(() => {
    void loadVisibleGroups(viewRef.current, "refresh")
    void refreshProjects().then(() =>
      loadVisibleGroups(viewRef.current, "ensure"),
    )
  }, [loadVisibleGroups, refreshProjects])

  const setArchived = useCallback(
    (value: boolean) => {
      const scope = value ? "archived" : "active"
      viewRef.current = scope
      setView(scope)
      void loadVisibleGroups(scope, "ensure")
    },
    [loadVisibleGroups],
  )

  const toggleProject = useCallback(
    (key: string) => {
      const expand = collapsedRef.current[key] !== false
      setCollapsed(key, !expand)
      if (expand) void loadGroup(key, "ensure")
    },
    [loadGroup, setCollapsed],
  )

  const loadMore = useCallback(
    (key: string) => {
      void loadGroup(key, "more")
    },
    [loadGroup],
  )

  const retryGroup = useCallback(
    (key: string) => {
      void loadGroup(key, "refresh")
    },
    [loadGroup],
  )

  const removeSession = useCallback(
    (scope: View, key: string, id: string) => {
      setGroups(scope, (previous) => {
        const group = previous[key]
        if (!group || !group.items.some((item) => item.id === id))
          return previous
        return {
          ...previous,
          [key]: {
            ...group,
            items: group.items.filter((item) => item.id !== id),
          },
        }
      })
    },
    [setGroups],
  )

  const handleDeleteSession = useCallback(
    async (id: string) => {
      setDeleteError(false)
      try {
        await deleteSession(id)
        for (const scope of ["active", "archived"] as const) {
          for (const [key, group] of Object.entries(viewsRef.current[scope])) {
            if (!group.items.some((item) => item.id === id)) continue
            removeSession(scope, key, id)
            void loadGroup(key, "refresh", scope)
          }
        }
        if (id === getChatState().activeSessionId) onDeletedActiveSession()
        void refreshProjects(true)
      } catch (error) {
        console.error("Failed to delete session:", error)
        setDeleteError(true)
      }
    },
    [loadGroup, onDeletedActiveSession, refreshProjects, removeSession],
  )

  const handleUpdateSession = useCallback(
    async (id: string, update: Parameters<typeof updateSessionMetadata>[1]) => {
      const scope = viewRef.current
      const current = Object.values(viewsRef.current[scope])
        .flatMap((group) => group.items)
        .find((item) => item.id === id)
      const oldKey = groupKey(current?.project_id)
      try {
        await updateSessionMetadata(id, update)
        if (id === getChatState().activeSessionId) {
          updateChatStore({
            ...(update.title !== undefined
              ? { activeSessionTitle: update.title }
              : {}),
            ...(update.project_id !== undefined
              ? { activeProjectId: update.project_id || undefined }
              : {}),
          })
        }
        if (update.title !== undefined && current) {
          const title = update.title
          setGroups(scope, (previous) => {
            const group = previous[oldKey]
            if (!group) return previous
            return {
              ...previous,
              [oldKey]: {
                ...group,
                items: group.items.map((item) =>
                  item.id === id ? { ...item, title } : item,
                ),
              },
            }
          })
          if (requests.current.has(`${scope}:${oldKey}`))
            void loadGroup(oldKey, "refresh", scope)
          return
        }
        const nextKey =
          update.project_id === undefined ? oldKey : groupKey(update.project_id)
        const nextScope =
          update.archived === undefined
            ? scope
            : update.archived
              ? "archived"
              : "active"
        if (nextKey !== oldKey || nextScope !== scope) {
          removeSession(scope, oldKey, id)
          if (update.project_id) setCollapsed(update.project_id, false)
          if (
            nextScope === viewRef.current ||
            viewsRef.current[nextScope][nextKey]
          ) {
            void loadGroup(nextKey, "refresh", nextScope)
          }
          void refreshProjects(true)
        }
        void loadGroup(oldKey, "refresh", scope)
      } catch (error) {
        console.error("Failed to update session:", error)
        toast.error(t("chat.sessionUpdateFailed"))
      }
    },
    [loadGroup, refreshProjects, removeSession, setCollapsed, setGroups, t],
  )

  const createChatProject = useCallback(
    async (name: string, instructions: string) => {
      const project = await createProject({ name, instructions })
      setProjectsValue(sortProjects([...projectsRef.current, project]))
      setCollapsed(project.id, false)
      void loadGroup(project.id, "ensure")
      return project
    },
    [loadGroup, setCollapsed, setProjectsValue],
  )

  const updateChatProject = useCallback(
    async (id: string, input: { name?: string; instructions?: string }) => {
      const project = await updateProject(id, input)
      setProjectsValue(
        sortProjects(
          projectsRef.current.map((item) => (item.id === id ? project : item)),
        ),
      )
      return project
    },
    [setProjectsValue],
  )

  const deleteChatProject = useCallback(
    async (id: string) => {
      await deleteProject(id)
      setProjectsValue(projectsRef.current.filter((item) => item.id !== id))
      if (getChatState().activeProjectId === id)
        updateChatStore({ activeProjectId: undefined })
      for (const scope of ["active", "archived"] as const) {
        const ticket = `${scope}:${id}`
        requests.current.get(ticket)?.abort()
        requests.current.delete(ticket)
        setGroups(scope, (previous) => {
          const next = { ...previous }
          delete next[id]
          return next
        })
        if (
          scope === viewRef.current ||
          viewsRef.current[scope][UNCATEGORIZED_KEY]
        ) {
          void loadGroup(UNCATEGORIZED_KEY, "refresh", scope)
        }
      }
      void refreshProjects(true)
    },
    [loadGroup, refreshProjects, setGroups, setProjectsValue],
  )

  return {
    projects,
    groups: views[view],
    collapsed,
    projectsLoading,
    projectsError,
    deleteError,
    archived: view === "archived",
    setArchived,
    refresh,
    loadMore,
    retryGroup,
    toggleProject,
    handleDeleteSession,
    handleUpdateSession,
    createChatProject,
    updateChatProject,
    deleteChatProject,
  }
}
