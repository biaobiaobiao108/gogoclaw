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
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [deleteError, setDeleteError] = useState(false)

  const loadSessions = useCallback(
    async (reset = true) => {
      try {
        const currentOffset = reset ? 0 : offset
        if (reset) {
          setLoadError(false)
          setIsLoading(true)
          setDeleteError(false)
          setHasMore(true)
          setOffset(0)
        }

        const data = await getSessions(currentOffset, LIMIT)
        setLoadError(false)

        if (data.length < LIMIT) {
          setHasMore(false)
        }

        if (reset) {
          setSessions(data)
        } else {
          setSessions((prev) => {
            const existingIds = new Set(prev.map((s) => s.id))
            const newItems = data.filter((s) => !existingIds.has(s.id))
            return [...prev, ...newItems]
          })
        }

        setOffset(currentOffset + data.length)
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
    [offset],
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
        const deletedLoadedSession = sessions.some(
          (session) => session.id === id,
        )
        await deleteSession(id)
        setSessions((prev) => prev.filter((s) => s.id !== id))
        if (deletedLoadedSession) {
          setOffset((prev) => Math.max(prev - 1, 0))
        }
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
