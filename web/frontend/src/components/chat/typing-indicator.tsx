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
    <div className="flex w-full items-center gap-3 py-3">
      <div className="flex items-center gap-2">
        <span className="bg-foreground/70 size-2 animate-bounce rounded-full [animation-delay:-0.3s]" />
        <span className="bg-foreground/70 size-2 animate-bounce rounded-full [animation-delay:-0.15s]" />
        <span className="bg-foreground/70 size-2 animate-bounce rounded-full" />
      </div>
      <span
        key={stepIndex}
        className="text-muted-foreground/80 animate-[fadeSlideIn_0.3s_ease-out] text-xs font-normal"
      >
        {thinkingSteps[stepIndex]}
      </span>
    </div>
  )
}
