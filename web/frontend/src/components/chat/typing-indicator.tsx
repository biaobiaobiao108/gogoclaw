import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

export function TypingIndicator() {
  const { t } = useTranslation()
  const thinkingSteps = [
    t("chat.thinking.step1"),
    t("chat.thinking.step2"),
    t("chat.thinking.step3"),
    t("chat.thinking.step4"),
  ]
  const [stepIndex, setStepIndex] = useState(0)

  useEffect(() => {
    const stepsCount = thinkingSteps.length
    const interval = setInterval(() => {
      setStepIndex((prev) => (prev + 1) % stepsCount)
    }, 2500)
    return () => clearInterval(interval)
  }, [thinkingSteps.length])

  return (
    <div className="flex w-full items-center gap-2 py-2">
      <div className="border-border/60 bg-muted/30 flex items-center gap-1.5 rounded-md border px-3 py-1.5 shadow-xs">
        <span className="bg-accent size-1.5 animate-pulse rounded-full" />
        <span className="bg-accent size-1.5 animate-pulse rounded-full [animation-delay:0.2s]" />
        <span className="bg-accent size-1.5 animate-pulse rounded-full [animation-delay:0.4s]" />
        <span
          key={stepIndex}
          className="text-muted-foreground ml-2 animate-[fadeSlideIn_0.3s_ease-out] font-mono text-xs"
        >
          {thinkingSteps[stepIndex]}
        </span>
      </div>
    </div>
  )
}
