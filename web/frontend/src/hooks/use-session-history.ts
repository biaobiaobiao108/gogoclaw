import { useAtomValue } from "jotai"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
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
  activeSessionId: string
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
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, boolean>)
      : {}
  } catch {
    return {}
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

export function useSessionHistory({
  activeSessionId,
  onDeletedActiveSession,
}: UseSessionHistoryOptions) {
  const { t } = useTranslation()
  const activeProjectId = useAtomValue(activeChatProjectIdAtom)
  const [archived, setArchivedState] = useState(false)
  const [projects, setProjects] = useState<ChatProject[]>([])
  const [projectsLoading, setProjectsLoading] = useState(true)
  const [projectsError, setProjectsError] = useState(false)
  const [groups, setGroupsState] = useState<
    Record<string, SessionHistoryGroupState>
  >(() => ({ [UNCATEGORIZED_KEY]: emptyGroup(UNCATEGORIZED_KEY) }))
  const [collapsed, setCollapsedState] =
    useState<Record<string, boolean>>(readCollapsedState)
  const [deleteError, setDeleteError] = useState(false)
  const groupsRef = useRef(groups)
  const projectsRef = useRef(projects)
  const collapsedRef = useRef(collapsed)
  const requestSeq = useRef<Record<string, number>>({})

  const setGroups = useCallback(
    (
      update: (
        previous: Record<string, SessionHistoryGroupState>,
      ) => Record<string, SessionHistoryGroupState>,
    ) => {
      setGroupsState((previous) => {
        const next = update(previous)
        groupsRef.current = next
        return next
      })
    },
    [],
  )

  const setProjectsValue = useCallback((next: ChatProject[]) => {
    projectsRef.current = next
    setProjects(next)
  }, [])

  const setCollapsed = useCallback(
    (
      update: (previous: Record<string, boolean>) => Record<string, boolean>,
    ) => {
      setCollapsedState((previous) => {
        const next = update(previous)
        collapsedRef.current = next
        writeCollapsedState(next)
        return next
      })
    },
    [],
  )

  const refreshProjects = useCallback(async () => {
    setProjectsLoading(true)
    setProjectsError(false)
    try {
      const next = sortProjects(await getProjects())
      setProjectsValue(next)
      setCollapsed((previous) => {
        const updated = { ...previous }
        if (updated[UNCATEGORIZED_KEY] === undefined) {
          updated[UNCATEGORIZED_KEY] = false
        }
        for (const project of next) {
          if (updated[project.id] === undefined) {
            updated[project.id] = project.id !== activeProjectId
          }
        }
        if (activeProjectId) updated[activeProjectId] = false
        return updated
      })
      return next
    } catch (error) {
      console.error("Failed to load chat projects:", error)
      setProjectsError(true)
      return projectsRef.current
    } finally {
      setProjectsLoading(false)
    }
  }, [activeProjectId, setCollapsed, setProjectsValue])

  const loadGroup = useCallback(
    async (key: string, reset = false) => {
      const current = groupsRef.current[key] ?? emptyGroup(key)
      if (!reset) {
        if (current.isLoading || (current.hasLoaded && !current.hasMore)) return
      }
      const requestId = (requestSeq.current[key] ?? 0) + 1
      requestSeq.current[key] = requestId
      const cursor = reset ? undefined : current.cursor
      setGroups((previous) => ({
        ...previous,
        [key]: {
          ...(previous[key] ?? emptyGroup(key)),
          isLoading: true,
          loadError: false,
          ...(reset
            ? {
                items: [],
                cursor: undefined,
                hasMore: true,
                hasLoaded: false,
              }
            : {}),
        },
      }))
      try {
        const data = await getSessions(
          cursor,
          LIMIT,
          archived,
          key === UNCATEGORIZED_KEY ? UNCATEGORIZED_KEY : key,
        )
        if (requestSeq.current[key] !== requestId) return
        const chat = getChatState()
        const activeTitle = data.items.find(
          (session) => session.id === chat.activeSessionId,
        )?.title
        if (activeTitle && activeTitle !== chat.activeSessionTitle) {
          updateChatStore({ activeSessionTitle: activeTitle })
        }
        setGroups((previous) => {
          const existing = previous[key] ?? emptyGroup(key)
          const items = reset
            ? data.items
            : [
                ...existing.items,
                ...data.items.filter(
                  (item) =>
                    !existing.items.some((entry) => entry.id === item.id),
                ),
              ]
          return {
            ...previous,
            [key]: {
              key,
              items,
              cursor: data.next_cursor,
              hasMore: data.has_more,
              hasLoaded: true,
              isLoading: false,
              loadError: false,
            },
          }
        })
      } catch (error) {
        if (requestSeq.current[key] !== requestId) return
        console.error("Failed to fetch chat group:", error)
        setGroups((previous) => ({
          ...previous,
          [key]: {
            ...(previous[key] ?? emptyGroup(key)),
            isLoading: false,
            loadError: true,
          },
        }))
      }
    },
    [archived, setGroups],
  )

  const visibleGroupKeys = useCallback(() => {
    const keys = new Set<string>([UNCATEGORIZED_KEY])
    for (const project of projectsRef.current) {
      if (collapsedRef.current[project.id] === false) keys.add(project.id)
    }
    if (activeProjectId) keys.add(activeProjectId)
    return [...keys]
  }, [activeProjectId])

  const refresh = useCallback(async () => {
    const nextProjects = await refreshProjects()
    const keys = new Set<string>([UNCATEGORIZED_KEY])
    for (const project of nextProjects) {
      if (collapsedRef.current[project.id] === false) keys.add(project.id)
    }
    if (activeProjectId) keys.add(activeProjectId)
    await Promise.all([...keys].map((key) => loadGroup(key, true)))
  }, [activeProjectId, loadGroup, refreshProjects])

  const setArchived = useCallback(
    (value: boolean) => {
      requestSeq.current = {}
      setGroups(() => ({ [UNCATEGORIZED_KEY]: emptyGroup(UNCATEGORIZED_KEY) }))
      setArchivedState(value)
    },
    [setGroups],
  )

  const toggleProject = useCallback(
    (key: string) => {
      const nextExpanded = collapsedRef.current[key] !== false
      setCollapsed((previous) => ({ ...previous, [key]: !nextExpanded }))
      if (nextExpanded) void loadGroup(key, false)
    },
    [loadGroup, setCollapsed],
  )

  const handleDeleteSession = useCallback(
    async (id: string) => {
      try {
        setDeleteError(false)
        await deleteSession(id)
        setGroups((previous) => {
          const next: Record<string, SessionHistoryGroupState> = {}
          for (const [key, group] of Object.entries(previous)) {
            next[key] = {
              ...group,
              items: group.items.filter((session) => session.id !== id),
            }
          }
          return next
        })
        await refreshProjects()
        if (id === activeSessionId) onDeletedActiveSession()
      } catch (error) {
        console.error("Failed to delete session:", error)
        setDeleteError(true)
      }
    },
    [activeSessionId, onDeletedActiveSession, refreshProjects, setGroups],
  )

  const handleUpdateSession = useCallback(
    async (
      id: string,
      update: {
        title?: string
        pinned?: boolean
        archived?: boolean
        project_id?: string | null
      },
    ) => {
      const previousGroups = groupsRef.current
      let previousProjectID: string | undefined
      for (const group of Object.values(previousGroups)) {
        const item = group.items.find((session) => session.id === id)
        if (item) {
          previousProjectID = item.project_id
          break
        }
      }
      if (update.title !== undefined && id === getChatState().activeSessionId) {
        updateChatStore({ activeSessionTitle: update.title })
      }
      try {
        await updateSessionMetadata(id, update)
        if (
          update.project_id !== undefined &&
          id === getChatState().activeSessionId
        ) {
          updateChatStore({
            activeProjectId: update.project_id || undefined,
          })
        }
        await refreshProjects()
        const affected = new Set<string>([UNCATEGORIZED_KEY])
        affected.add(groupKey(previousProjectID))
        affected.add(groupKey(update.project_id))
        if (update.project_id) {
          setCollapsed((previous) => ({
            ...previous,
            [update.project_id as string]: false,
          }))
        }
        await Promise.all([...affected].map((key) => loadGroup(key, true)))
        if (update.title !== undefined) await refresh()
      } catch (error) {
        console.error("Failed to update session:", error)
        toast.error(t("chat.sessionUpdateFailed"))
        await refresh()
      }
    },
    [loadGroup, refresh, refreshProjects, setCollapsed, t],
  )

  const createChatProject = useCallback(
    async (name: string, instructions: string) => {
      const project = await createProject({ name, instructions })
      setProjectsValue(sortProjects([...projectsRef.current, project]))
      setCollapsed((previous) => ({ ...previous, [project.id]: false }))
      await loadGroup(project.id, true)
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
      setGroups((previous) => {
        const next = { ...previous }
        delete next[id]
        next[UNCATEGORIZED_KEY] = emptyGroup(UNCATEGORIZED_KEY)
        return next
      })
      await Promise.all([loadGroup(UNCATEGORIZED_KEY, true), refreshProjects()])
    },
    [loadGroup, refreshProjects, setGroups, setProjectsValue],
  )

  useEffect(() => {
    groupsRef.current = groups
  }, [groups])

  useEffect(() => {
    projectsRef.current = projects
  }, [projects])

  useEffect(() => {
    void refresh()
    // Refresh is intentionally tied to archive mode and active project only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [archived, activeProjectId])

  useEffect(() => {
    if (!activeProjectId) return
    setCollapsed((previous) =>
      previous[activeProjectId] === false
        ? previous
        : { ...previous, [activeProjectId]: false },
    )
    const group = groupsRef.current[activeProjectId]
    if (!group?.hasLoaded) void loadGroup(activeProjectId, false)
  }, [activeProjectId, loadGroup, setCollapsed])

  const isLoading = useMemo(() => {
    if (projectsLoading) return true
    const keys = new Set(visibleGroupKeys())
    return [...keys].some((key) => groups[key]?.isLoading)
  }, [groups, projectsLoading, visibleGroupKeys])

  const loadError = useMemo(() => {
    if (projectsError) return true
    const keys = new Set(visibleGroupKeys())
    return [...keys].some((key) => groups[key]?.loadError)
  }, [groups, projectsError, visibleGroupKeys])

  return {
    projects,
    groups,
    collapsed,
    projectsLoading,
    projectsError,
    isLoading,
    loadError,
    loadErrorMessage: t("chat.historyLoadFailed"),
    deleteError,
    archived,
    setArchived,
    loadSessions: refresh,
    loadGroup,
    toggleProject,
    handleDeleteSession,
    handleUpdateSession,
    createChatProject,
    updateChatProject,
    deleteChatProject,
  }
}
