import { useCallback, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { deleteSession, getSessions, type SessionSummary } from "@/api/sessions"

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
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [cursor, setCursor] = useState<string>()
  const [hasMore, setHasMore] = useState(true)
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [deleteError, setDeleteError] = useState(false)

  const loadSessions = useCallback(
    async (reset = true) => {
      if (loadingRef.current) return
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

        const data = await getSessions(currentCursor, LIMIT)
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
        console.error("Failed to fetch session history:", err)
        setLoadError(true)
      } finally {
        setIsLoading(false)
        loadingRef.current = false
      }
    },
    [cursor],
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

  return {
    sessions,
    hasMore,
    loadError,
    isLoading,
    deleteError,
    loadErrorMessage: t("chat.historyLoadFailed"),
    loadSessions,
    handleDeleteSession,
  }
}
