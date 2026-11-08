import { useCallback, useEffect, useRef, useState } from "react"
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
  const observerRef = useRef<HTMLDivElement>(null)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [cursor, setCursor] = useState<string>()
  const [hasMore, setHasMore] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [deleteError, setDeleteError] = useState(false)

  const loadSessions = useCallback(
    async (reset = true) => {
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
        if (!reset) {
          setHasMore(false)
        }
      } finally {
        setIsLoading(false)
        setIsLoadingMore(false)
      }
    },
    [cursor],
  )

  useEffect(() => {
    if (!observerRef.current || !hasMore || isLoadingMore || loadError) return

    const observer = new IntersectionObserver(
      (entries) => {
        if (
          entries[0].isIntersecting &&
          hasMore &&
          !isLoadingMore &&
          !loadError
        ) {
          setIsLoadingMore(true)
          void loadSessions(false)
        }
      },
      { threshold: 0.1 },
    )

    observer.observe(observerRef.current)
    return () => observer.disconnect()
  }, [hasMore, isLoadingMore, loadError, loadSessions])

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
    [activeSessionId, onDeletedActiveSession, sessions],
  )

  return {
    sessions,
    hasMore,
    loadError,
    isLoading,
    deleteError,
    loadErrorMessage: t("chat.historyLoadFailed"),
    observerRef,
    loadSessions,
    handleDeleteSession,
  }
}
