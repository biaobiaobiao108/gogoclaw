import { useCallback, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  deleteSession,
  getSessions,
  type SessionSummary,
  updateSessionMetadata,
} from "@/api/sessions"

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
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [cursor, setCursor] = useState<string>()
  const [hasMore, setHasMore] = useState(true)
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [deleteError, setDeleteError] = useState(false)
  const [archived, setArchivedState] = useState(false)

  const setArchived = useCallback((value: boolean) => {
    setArchivedState(value)
    setCursor(undefined)
    setHasMore(true)
    setSessions([])
  }, [])

  const loadSessions = useCallback(
    async (reset = true) => {
      if (!reset && loadingRef.current) return
      const requestId = ++requestIdRef.current
      loadingRef.current = true
      setIsLoading(true)
      setLoadError(false)
      try {
        const currentCursor = reset ? undefined : cursor
        if (reset) {
          setLoadError(false)
          setIsLoading(true)
          setDeleteError(false)
          setHasMore(true)
          setCursor(undefined)
        }

        const data = await getSessions(currentCursor, LIMIT, archived)
        if (requestId !== requestIdRef.current) return
        setLoadError(false)

        setHasMore(data.has_more)
        setCursor(data.next_cursor)

        if (reset) {
          setSessions(data.items)
        } else {
          setSessions((prev) => {
            const existingIds = new Set(prev.map((s) => s.id))
            const newItems = data.items.filter((s) => !existingIds.has(s.id))
            return [...prev, ...newItems]
          })
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
    [archived, cursor],
  )

  const handleDeleteSession = useCallback(
    async (id: string) => {
      try {
        setDeleteError(false)
        await deleteSession(id)
        setSessions((prev) => prev.filter((s) => s.id !== id))
        if (id === activeSessionId) {
          onDeletedActiveSession()
        }
      } catch (err) {
        console.error("Failed to delete session:", err)
        setDeleteError(true)
      }
    },
    [activeSessionId, onDeletedActiveSession],
  )

  const handleUpdateSession = useCallback(
    async (
      id: string,
      update: { title?: string; pinned?: boolean; archived?: boolean },
    ) => {
      setSessions((previous) => {
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
              Boolean(session.archived) === archived,
          )
        return next.sort((left, right) => {
          if (Boolean(left.pinned) !== Boolean(right.pinned)) {
            return left.pinned ? -1 : 1
          }
          return Date.parse(right.updated) - Date.parse(left.updated)
        })
      })
      try {
        await updateSessionMetadata(id, update)
      } catch (err) {
        console.error("Failed to update session:", err)
        toast.error(t("chat.sessionUpdateFailed"))
        await loadSessions(true)
      }
    },
    [archived, loadSessions, t],
  )

  return {
    sessions,
    hasMore,
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
