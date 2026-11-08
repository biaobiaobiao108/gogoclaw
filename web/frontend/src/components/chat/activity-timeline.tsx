import {
  IconCheck,
  IconCircle,
  IconClock,
  IconTool,
  IconX,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { MessageCodeBlock } from "@/components/chat/message-code-block"
import type { ChatActivity, ChatPlanStep, ChatTurnHistory } from "@/store/chat"

function statusLabelKey(status: string): string {
  if (status === "completed") return "chat.activityStatus.completed"
  if (status === "failed") return "chat.activityStatus.failed"
  if (status === "running") return "chat.activityStatus.running"
  return "chat.activityStatus.pending"
}

function ActivityIcon({ status }: { status: string }) {
  if (status === "completed") {
    return <IconCheck className="text-success size-3.5" />
  }
  if (status === "failed") {
    return <IconX className="text-destructive size-3.5" />
  }
  if (status === "running") {
    return <IconClock className="text-accent size-3.5" />
  }
  return <IconCircle className="text-muted-foreground size-3.5" />
}

function ActivityContent({
  plan = [],
  activities = [],
}: {
  plan?: ChatPlanStep[]
  activities?: ChatActivity[]
}) {
  const { t } = useTranslation()
  return (
    <>
      {plan.length > 0 && (
        <ol className="border-border/50 mt-2 space-y-1.5 border-t pt-2">
          {plan.map((step, index) => (
            <li
              key={`${step.text}-${index}`}
              className="text-muted-foreground flex gap-2"
            >
              <ActivityIcon status={step.status} />
              <span>{step.text}</span>
            </li>
          ))}
        </ol>
      )}

      {activities.length > 0 && (
        <div className="border-border/50 mt-2 space-y-2 border-t pt-2">
          {activities.map((activity) => (
            <details key={activity.id}>
              <summary className="flex cursor-pointer list-none items-center gap-2 [&::-webkit-details-marker]:hidden">
                <ActivityIcon status={activity.status} />
                <IconTool className="text-muted-foreground size-3" />
                <span className="text-foreground/90 min-w-0 flex-1 truncate font-mono">
                  {activity.title}
                </span>
                {typeof activity.durationMs === "number" && (
                  <span className="text-muted-foreground tabular-nums">
                    {activity.durationMs} ms
                  </span>
                )}
                <span className="text-muted-foreground">
                  {t(statusLabelKey(activity.status))}
                </span>
              </summary>
              {(activity.output || activity.diff) && (
                <div className="mt-2 space-y-2 pl-5">
                  {activity.diff && (
                    <MessageCodeBlock
                      code={activity.diff}
                      language="diff"
                      className="border-border/50 text-[11px]"
                      bodyClassName="max-h-80 overflow-auto p-2"
                    />
                  )}
                  {activity.output && (
                    <pre className="bg-background/60 border-border/50 max-h-64 overflow-auto rounded-md border p-2 font-mono text-[11px] whitespace-pre-wrap">
                      {activity.output}
                    </pre>
                  )}
                </div>
              )}
            </details>
          ))}
        </div>
      )}
    </>
  )
}

export function ActivityTimeline({
  plan = [],
  activities = [],
  history = [],
}: {
  plan?: ChatPlanStep[]
  activities?: ChatActivity[]
  history?: ChatTurnHistory[]
}) {
  const { t } = useTranslation()
  const priorTurns = history
    .filter((turn) => turn.plan.length > 0 || turn.activities.length > 0)
    .slice(-20)
    .reverse()
  const activityCount =
    activities.length +
    priorTurns.reduce((count, turn) => count + turn.activities.length, 0)
  if (plan.length === 0 && activities.length === 0 && priorTurns.length === 0)
    return null

  return (
    <details className="border-border/60 bg-muted/10 rounded-lg border px-3 py-2 text-xs">
      <summary className="text-muted-foreground cursor-pointer font-mono select-none">
        {t("chat.activityTimeline")}
        {activityCount > 0 && (
          <span className="text-muted-foreground ml-2 tabular-nums">
            {activityCount}
          </span>
        )}
      </summary>

      <ActivityContent plan={plan} activities={activities} />

      {priorTurns.length > 0 && (
        <div className="border-border/50 mt-3 space-y-2 border-t pt-2">
          <div className="text-muted-foreground">{t("chat.turnHistory")}</div>
          {priorTurns.map((turn) => (
            <details
              key={turn.turnId}
              className="bg-background/40 border-border/50 rounded-md border px-2 py-1.5"
            >
              <summary className="text-muted-foreground cursor-pointer select-none">
                {t("chat.turnLabel", { iteration: turn.iteration })}
              </summary>
              <ActivityContent plan={turn.plan} activities={turn.activities} />
            </details>
          ))}
        </div>
      )}
    </details>
  )
}
