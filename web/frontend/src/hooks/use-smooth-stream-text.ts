import { useEffect, useRef, useState } from "react"

import { smoothRevealCount, smoothRevealDuration } from "@/lib/smooth-stream"

const FRAME_INTERVAL_MS = 24

export function useSmoothStreamText(content: string, isStreaming: boolean) {
  const [visibleContent, setVisibleContent] = useState(content)
  const charsRef = useRef(Array.from(content))
  const visibleCountRef = useRef(charsRef.current.length)
  const frameRef = useRef<number | null>(null)
  const lastPaintRef = useRef(0)
  const fromRef = useRef(visibleCountRef.current)
  const toRef = useRef(charsRef.current.length)
  const startedAtRef = useRef(0)
  const durationRef = useRef(0)

  useEffect(() => {
    charsRef.current = Array.from(content)
    const targetCount = charsRef.current.length

    const stopAnimation = () => {
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current)
        frameRef.current = null
      }
    }

    if (!isStreaming) {
      stopAnimation()
      visibleCountRef.current = targetCount
      setVisibleContent(content)
      return
    }

    if (targetCount <= visibleCountRef.current) {
      stopAnimation()
      visibleCountRef.current = targetCount
      setVisibleContent(content)
      return
    }

    fromRef.current = visibleCountRef.current
    toRef.current = targetCount
    startedAtRef.current = performance.now()
    durationRef.current = smoothRevealDuration(fromRef.current, toRef.current)
    lastPaintRef.current = 0

    const tick = (now: number) => {
      if (now - lastPaintRef.current >= FRAME_INTERVAL_MS) {
        const progress =
          durationRef.current > 0
            ? (now - startedAtRef.current) / durationRef.current
            : 1
        const nextCount = smoothRevealCount(
          fromRef.current,
          toRef.current,
          progress,
        )
        if (nextCount > visibleCountRef.current) {
          visibleCountRef.current = nextCount
          setVisibleContent(charsRef.current.slice(0, nextCount).join(""))
        }
        lastPaintRef.current = now
      }

      if (visibleCountRef.current < toRef.current) {
        frameRef.current = window.requestAnimationFrame(tick)
      } else {
        frameRef.current = null
      }
    }

    if (frameRef.current === null) {
      frameRef.current = window.requestAnimationFrame(tick)
    }
  }, [content, isStreaming])

  useEffect(
    () => () => {
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current)
      }
    },
    [],
  )

  return visibleContent
}
