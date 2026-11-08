import { IconShieldExclamation } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import type { ChatApproval } from "@/store/chat"

function approvalRiskKey(tool: string) {
  switch (tool) {
    case "exec":
      return "chat.approvalRiskExec"
    case "write_file":
    case "edit_file":
    case "append_file":
      return "chat.approvalRiskFiles"
    case "spawn":
    case "subagent":
      return "chat.approvalRiskAgent"
    case "install_skill":
      return "chat.approvalRiskSkill"
    default:
      return "chat.approvalRiskFallback"
  }
}

export function ApprovalPanel({
  approval,
  onRespond,
}: {
  approval: ChatApproval
  onRespond: (input: {
    approve: boolean
    scope?: "once" | "session"
  }) => boolean
}) {
  const { t } = useTranslation()
  const args = approval.arguments
    ? JSON.stringify(approval.arguments, null, 2)
    : ""
  const busy = Boolean(approval.isResponding)

  return (
    <section
      aria-labelledby="chat-approval-title"
      aria-live="polite"
      className="border-border bg-muted/20 mx-auto mb-2 w-full max-w-4xl rounded-lg border px-4 py-3"
    >
      <div className="flex items-start gap-3">
        <IconShieldExclamation
          className="text-accent mt-0.5 size-4 shrink-0"
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <h2 id="chat-approval-title" className="text-sm font-medium">
              {t("chat.approvalTitle")}
            </h2>
            <code className="text-muted-foreground font-mono text-xs">
              {approval.tool}
            </code>
          </div>
          <p className="text-muted-foreground mt-1 text-xs leading-relaxed">
            {approval.risk || t(approvalRiskKey(approval.tool))}
          </p>
          {args && (
            <details className="mt-2">
              <summary className="text-muted-foreground cursor-pointer text-xs select-none">
                {t("chat.approvalArguments")}
              </summary>
              <pre className="bg-background/70 border-border/60 mt-2 max-h-52 overflow-auto rounded-md border p-2 font-mono text-[11px] whitespace-pre-wrap">
                {args}
              </pre>
            </details>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="sm"
              disabled={busy}
              onClick={() => onRespond({ approve: true, scope: "once" })}
            >
              {t("chat.approvalOnce")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => onRespond({ approve: true, scope: "session" })}
            >
              {t("chat.approvalSession")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => onRespond({ approve: false })}
              className="text-muted-foreground hover:text-destructive"
            >
              {t("chat.approvalDeny")}
            </Button>
            {busy && (
              <span className="text-muted-foreground text-xs" role="status">
                {t("chat.approvalSending")}
              </span>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
