import { IconArrowDown } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { ApprovalPanel } from "@/components/chat/approval-panel"
import { Button } from "@/components/ui/button"
import type { ChatApproval, ChatHistoryTurn, ChatPlanStep } from "@/store/chat"

export function ChatTaskPlan({
  steps,
}: {
  steps: { status: string; text: string }[]
}) {
  const { t } = useTranslation()
  if (steps.length === 0) return null

  return (
    <details
      open
      className="mx-auto max-h-[25dvh] w-full max-w-3xl shrink-0 overflow-y-auto px-4 pb-2 text-xs"
    >
      <summary className="text-muted-foreground cursor-pointer">
        {t("chat.taskPlan")}
      </summary>
      <ol className="mt-2 space-y-1 pl-4">
        {steps.map((step, index) => (
          <li key={index} className="text-muted-foreground">
            <span aria-label={t(`chat.planStatus.${step.status}`)}>
              {step.status === "completed"
                ? "✓"
                : step.status === "in_progress"
                  ? "●"
                  : "○"}
            </span>{" "}
            {step.text}
          </li>
        ))}
      </ol>
    </details>
  )
}

export function ChatInterruptedNotice({
  tools = [],
  onContinue,
}: {
  tools?: ChatHistoryTurn["recoveryTools"]
  onContinue: () => void
}) {
  const { t } = useTranslation()
  const completed = tools.filter((item) => item.outcome === "completed")
  const unknown = tools.filter((item) => item.outcome === "unknown")

  return (
    <div className="mx-auto flex w-full max-w-3xl items-center justify-between gap-3 px-4 pb-2 text-xs">
      <div className="text-muted-foreground min-w-0 space-y-1 break-words">
        <p>{t("chat.taskInterrupted")}</p>
        {completed.length > 0 && (
          <p>
            {t("recovery.chat.completed", {
              tools: completed.map((item) => item.name).join(", "),
            })}
          </p>
        )}
        {unknown.length > 0 && (
          <p>
            {t("recovery.chat.unknown", {
              tools: unknown.map((item) => item.name).join(", "),
            })}
          </p>
        )}
        <p>{t("recovery.chat.continueHint")}</p>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={onContinue}>
        {t("chat.continueTask")}
      </Button>
    </div>
  )
}

export function ChatTurnControls({
  plan,
  approval,
  interrupted,
  historyTurn,
  onContinue,
  onRespond,
}: {
  plan?: ChatPlanStep[]
  approval: ChatApproval | null | undefined
  interrupted: boolean
  historyTurn?: ChatHistoryTurn
  onContinue: () => void
  onRespond: React.ComponentProps<typeof ApprovalPanel>["onRespond"]
}) {
  const recoverableTurn =
    historyTurn?.status === "running" || historyTurn?.status === "interrupted"
  return (
    <>
      {plan && <ChatTaskPlan steps={plan} />}
      {approval ? (
        <ApprovalPanel approval={approval} onRespond={onRespond} />
      ) : (
        interrupted && (
          <ChatInterruptedNotice
            tools={recoverableTurn ? historyTurn?.recoveryTools : undefined}
            onContinue={onContinue}
          />
        )
      )}
    </>
  )
}

export function ChatScrollToBottom({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation()

  return (
    <button
      type="button"
      className="bg-card text-foreground hover:bg-muted border-border/80 absolute right-6 bottom-36 z-30 flex size-9 cursor-pointer items-center justify-center rounded-full border shadow-xs transition-[color,background-color,border-color,box-shadow,transform] duration-[var(--motion-state)] ease-out active:scale-95 motion-reduce:transition-none"
      aria-label={t("chat.scrollToBottom")}
      onClick={onClick}
    >
      <IconArrowDown className="size-4" />
    </button>
  )
}
