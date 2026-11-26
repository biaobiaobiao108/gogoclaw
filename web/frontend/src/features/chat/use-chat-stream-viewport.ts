import { useVirtualizer } from "@tanstack/react-virtual"
import {
  type UIEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"

import {
  type ChatStreamRow,
  chatStreamRowKey,
  estimateChatStreamRow,
} from "@/components/chat/chat-page-logic"
import {
  getCachedRowSize,
  getSessionViewport,
  saveSessionViewport,
  setCachedRowSize,
} from "@/features/chat/viewport"
import { getChatState } from "@/store/chat"

interface UseChatStreamViewportOptions {
  activeSessionId: string
  streamRows: ChatStreamRow[]
  loadOlderHistory: () => Promise<boolean>
}

export function useChatStreamViewport({
  activeSessionId,
  streamRows,
  loadOlderHistory,
}: UseChatStreamViewportOptions) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const streamContentRef = useRef<HTMLDivElement>(null)

  const initialViewport = useMemo(
    () => getSessionViewport(activeSessionId),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )

  const [expandedProcesses, setExpandedProcesses] = useState<
    Record<string, boolean>
  >(() => initialViewport?.expandedProcesses ?? {})
  const [isAtBottom, setIsAtBottom] = useState(
    () => initialViewport?.isAtBottom ?? true,
  )
  const [hasScrolled, setHasScrolled] = useState(
    () => initialViewport?.hasScrolled ?? false,
  )

  const autoScrollEnabledRef = useRef(
    initialViewport?.autoScrollEnabled ?? true,
  )
  const prevSessionIdRef = useRef(activeSessionId)
  const isInitialMountRef = useRef(true)

  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTo({
      top: el.scrollHeight,
      behavior,
    })
    setIsAtBottom(true)
    autoScrollEnabledRef.current = true
  }, [])

  const syncScrollState = useCallback(
    (element: HTMLDivElement) => {
      const { clientHeight, scrollHeight, scrollTop } = element
      const atBottom = scrollHeight - scrollTop - clientHeight <= 48
      const scrolled = scrollTop > 0
      setHasScrolled(scrolled)
      setIsAtBottom(atBottom)
      if (atBottom) autoScrollEnabledRef.current = true

      saveSessionViewport(activeSessionId, {
        scrollTop,
        isAtBottom: atBottom,
        hasScrolled: scrolled,
        autoScrollEnabled: autoScrollEnabledRef.current,
        expandedProcesses,
      })
    },
    [activeSessionId, expandedProcesses],
  )

  useEffect(() => {
    const el = scrollRef.current
    return () => {
      if (el) {
        const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= 48
        saveSessionViewport(activeSessionId, {
          scrollTop: el.scrollTop,
          isAtBottom: atBottom,
          hasScrolled: el.scrollTop > 0,
          autoScrollEnabled: autoScrollEnabledRef.current,
          expandedProcesses,
        })
      }
    }
  }, [activeSessionId, expandedProcesses])

  const handleScroll = useCallback(
    (e: UIEvent<HTMLDivElement>) => {
      syncScrollState(e.currentTarget)
    },
    [syncScrollState],
  )

  const handleLoadOlderHistory = useCallback(async () => {
    const sessionId = activeSessionId
    const element = scrollRef.current
    const preserveFromBottom = element
      ? element.scrollHeight - element.scrollTop
      : 0
    const loaded = await loadOlderHistory()
    if (!loaded || !element || getChatState().activeSessionId !== sessionId) {
      return
    }
    requestAnimationFrame(() => {
      element.scrollTop = element.scrollHeight - preserveFromBottom
    })
  }, [activeSessionId, loadOlderHistory])

  const streamVirtualizer = useVirtualizer({
    count: streamRows.length,
    getScrollElement: () => scrollRef.current,
    initialOffset: initialViewport?.scrollTop,
    estimateSize: (index) => {
      const row = streamRows[index]
      if (!row) return 112
      const key = chatStreamRowKey(row)
      return getCachedRowSize(key, estimateChatStreamRow(row))
    },
    getItemKey: (index) => {
      const row = streamRows[index]
      return row ? chatStreamRowKey(row) : index
    },
    overscan: 6,
  })

  const handleMeasureElement = useCallback(
    (node: HTMLElement | null) => {
      streamVirtualizer.measureElement(node)
      if (node) {
        const indexStr = node.getAttribute("data-index")
        if (indexStr !== null) {
          const index = Number(indexStr)
          const row = streamRows[index]
          if (row) {
            const key = chatStreamRowKey(row)
            const height = node.getBoundingClientRect().height
            if (height > 0) {
              setCachedRowSize(key, height)
            }
          }
        }
      }
    },
    [streamRows, streamVirtualizer],
  )

  // Follow measured height changes, including image loading and text reveal.
  useEffect(() => {
    const content = streamContentRef.current
    const scroll = scrollRef.current
    if (!content || !scroll) return
    let frame = 0
    const follow = () => {
      if (!autoScrollEnabledRef.current) return
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        if (autoScrollEnabledRef.current) scroll.scrollTop = scroll.scrollHeight
      })
    }
    const observer = new ResizeObserver(follow)
    observer.observe(content)
    observer.observe(scroll)
    follow()
    return () => {
      observer.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [activeSessionId])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return

    const sessionChanged = prevSessionIdRef.current !== activeSessionId
    prevSessionIdRef.current = activeSessionId

    if (sessionChanged) {
      const targetViewport = getSessionViewport(activeSessionId)
      if (targetViewport) {
        if (targetViewport.isAtBottom) {
          el.scrollTop = el.scrollHeight
          setIsAtBottom(true)
          autoScrollEnabledRef.current = true
        } else {
          el.scrollTop = targetViewport.scrollTop
          setIsAtBottom(targetViewport.isAtBottom)
          autoScrollEnabledRef.current = targetViewport.autoScrollEnabled
        }
        setHasScrolled(targetViewport.hasScrolled)
        setExpandedProcesses(targetViewport.expandedProcesses)
      } else {
        el.scrollTop = el.scrollHeight
        setIsAtBottom(true)
        autoScrollEnabledRef.current = true
        setHasScrolled(false)
        setExpandedProcesses({})
      }
      return
    }

    if (isInitialMountRef.current) {
      isInitialMountRef.current = false
      if (initialViewport) {
        if (initialViewport.isAtBottom) {
          el.scrollTop = el.scrollHeight
        } else {
          el.scrollTop = initialViewport.scrollTop
        }
      } else {
        el.scrollTop = el.scrollHeight
      }
    }
  }, [activeSessionId, initialViewport])

  return {
    scrollRef,
    streamContentRef,
    autoScrollEnabledRef,
    expandedProcesses,
    setExpandedProcesses,
    isAtBottom,
    hasScrolled,
    scrollToBottom,
    handleScroll,
    handleLoadOlderHistory,
    streamVirtualizer,
    handleMeasureElement,
  }
}
