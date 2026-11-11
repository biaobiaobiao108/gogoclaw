import { useAtom } from "jotai"
import { useCallback, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  deleteSession,
  getSessions,
  type SessionSummary,
  updateSessionMetadata,
} from "@/api/sessions"
import {
  activeSessionHistoryAtom,
  archivedSessionHistoryAtom,
} from "@/store/chat"

const LIMIT = 20

interface UseSessionHistoryOptions {
  activeSessionId: string
  onDeletedActiveSession: () => void
}

export function useSessionHistory({
  activeSessionId,
  onDeletedActiveSession,
}: UseSessionHistoryOptions) {
  const { t } = useTranslation()
  const loadingRef = useRef(false)
  const requestIdRef = useRef(0)
  const [archived, setArchivedState] = useState(false)
  const [activeHistory, setActiveHistory] = useAtom(activeSessionHistoryAtom)
  const [archivedHistory, setArchivedHistory] = useAtom(
    archivedSessionHistoryAtom,
  )

  const currentCategory = archived ? archivedHistory : activeHistory
  const [isLoading, setIsLoading] = useState(!currentCategory.hasLoaded)
  const [loadError, setLoadError] = useState(false)
  const [deleteError, setDeleteError] = useState(false)

  const setArchived = useCallback((value: boolean) => {
    setArchivedState(value)
  }, [])

  const loadSessions = useCallback(
    async (reset = true) => {
      if (!reset && loadingRef.current) return
      const requestId = ++requestIdRef.current
      loadingRef.current = true

      const categoryLoaded = archived
        ? archivedHistory.hasLoaded
        : activeHistory.hasLoaded
      if (!categoryLoaded && reset) {
        setIsLoading(true)
      }
      setLoadError(false)
      try {
        const currentCursor = reset
          ? undefined
          : archived
            ? archivedHistory.cursor
            : activeHistory.cursor
        if (reset) {
          setLoadError(false)
          setDeleteError(false)
        }

        const data = await getSessions(currentCursor, LIMIT, archived)
        if (requestId !== requestIdRef.current) return
        setLoadError(false)

        if (archived) {
          setArchivedHistory((prev) => ({
            items: reset
              ? data.items
              : [
                  ...prev.items,
                  ...data.items.filter(
                    (s) => !prev.items.some((e) => e.id === s.id),
                  ),
                ],
            cursor: data.next_cursor,
            hasMore: data.has_more,
            hasLoaded: true,
          }))
        } else {
          setActiveHistory((prev) => ({
            items: reset
              ? data.items
              : [
                  ...prev.items,
                  ...data.items.filter(
                    (s) => !prev.items.some((e) => e.id === s.id),
                  ),
                ],
            cursor: data.next_cursor,
            hasMore: data.has_more,
            hasLoaded: true,
          }))
        }
      } catch (err) {
        if (requestId !== requestIdRef.current) return
        console.error("Failed to fetch session history:", err)
        setLoadError(true)
      } finally {
        if (requestId === requestIdRef.current) {
          setIsLoading(false)
          loadingRef.current = false
        }
      }
    },
    [
      activeHistory.cursor,
      activeHistory.hasLoaded,
      archived,
      archivedHistory.cursor,
      archivedHistory.hasLoaded,
      setActiveHistory,
      setArchivedHistory,
    ],
  )

  const handleDeleteSession = useCallback(
    async (id: string) => {
      try {
        setDeleteError(false)
        await deleteSession(id)
        setActiveHistory((prev) => ({
          ...prev,
          items: prev.items.filter((s) => s.id !== id),
        }))
        setArchivedHistory((prev) => ({
          ...prev,
          items: prev.items.filter((s) => s.id !== id),
        }))
        if (id === activeSessionId) {
          onDeletedActiveSession()
        }
      } catch (err) {
        console.error("Failed to delete session:", err)
        setDeleteError(true)
      }
    },
    [
      activeSessionId,
      onDeletedActiveSession,
      setActiveHistory,
      setArchivedHistory,
    ],
  )

  const handleUpdateSession = useCallback(
    async (
      id: string,
      update: { title?: string; pinned?: boolean; archived?: boolean },
    ) => {
      const updater = (
        previous: SessionSummary[],
        targetArchived: boolean,
      ): SessionSummary[] => {
        const next = previous
          .map((session) =>
            session.id === id
              ? {
                  ...session,
                  ...(update.title !== undefined
                    ? { title: update.title }
                    : {}),
                  ...(update.pinned !== undefined
                    ? { pinned: update.pinned }
                    : {}),
                  ...(update.archived !== undefined
                    ? { archived: update.archived }
                    : {}),
                }
              : session,
          )
          .filter(
            (session) =>
              update.archived === undefined ||
              session.id !== id ||
              Boolean(session.archived) === targetArchived,
          )
        return next.sort((left, right) => {
          if (Boolean(left.pinned) !== Boolean(right.pinned)) {
            return left.pinned ? -1 : 1
          }
          return Date.parse(right.updated) - Date.parse(left.updated)
        })
      }

      setActiveHistory((prev) => ({
        ...prev,
        items: updater(prev.items, false),
      }))
      setArchivedHistory((prev) => ({
        ...prev,
        items: updater(prev.items, true),
      }))

      try {
        await updateSessionMetadata(id, update)
      } catch (err) {
        console.error("Failed to update session:", err)
        toast.error(t("chat.sessionUpdateFailed"))
        await loadSessions(true)
      }
    },
    [loadSessions, setActiveHistory, setArchivedHistory, t],
  )

  return {
    sessions: currentCategory.items,
    hasMore: currentCategory.hasMore,
    loadError,
    isLoading,
    deleteError,
    archived,
    setArchived,
    loadErrorMessage: t("chat.historyLoadFailed"),
    loadSessions,
    handleDeleteSession,
    handleUpdateSession,
  }
}
