import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  getEvolution,
  getEvolutionReview,
  updateEvolution,
} from "@/api/evolution"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"

const evolutionKey = ["evolution"] as const

export function EvolutionReviewDialog() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          {t("pages.config.evolution_review_title")}
        </Button>
      </DialogTrigger>
      <DialogContent className="min-w-0 sm:max-w-2xl">
        <DialogHeader className="pr-8">
          <DialogTitle>{t("pages.config.evolution_review_title")}</DialogTitle>
          <DialogDescription>
            {t("pages.config.evolution_review_hint")}
          </DialogDescription>
        </DialogHeader>
        {open && <EvolutionReviewContent />}
      </DialogContent>
    </Dialog>
  )
}

function EvolutionReviewContent() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState("")
  const [confirmRollback, setConfirmRollback] = useState(false)
  const overview = useQuery({
    queryKey: evolutionKey,
    queryFn: ({ signal }) => getEvolution(signal),
    refetchInterval: (query) => (query.state.data?.run.running ? 1000 : 15000),
    retry: false,
  })
  const review = useQuery({
    queryKey: [...evolutionKey, selected],
    queryFn: ({ signal }) => getEvolutionReview(selected, signal),
    enabled: Boolean(selected),
    retry: false,
  })
  const mutation = useMutation({
    mutationFn: updateEvolution,
    onSuccess: async () => {
      setConfirmRollback(false)
      await queryClient.invalidateQueries({ queryKey: evolutionKey })
    },
  })

  const data = overview.data
  const detail = review.data
  const busy = mutation.isPending || data?.run.running
  const canRun = data?.mode === "apply" || data?.mode === "draft"

  if (overview.isPending) {
    return <Skeleton className="h-16 w-full" />
  }
  if (overview.isError || !data) {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <p role="alert">{t("pages.config.evolution_load_error")}</p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void overview.refetch()}
        >
          {t("pages.config.evolution_retry")}
        </Button>
      </div>
    )
  }

  return (
    <div className="flex max-h-[68dvh] min-w-0 flex-col gap-4 overflow-auto pr-1">
      <div className="flex flex-wrap items-center gap-3">
        <Button
          size="sm"
          disabled={!canRun || busy}
          onClick={() => mutation.mutate({ action: "run" })}
        >
          {t("pages.config.evolution_run")}
        </Button>
        <p className="text-muted-foreground text-xs" role="status">
          {data.run.running
            ? t("pages.config.evolution_running")
            : !canRun
              ? t("pages.config.evolution_run_disabled")
              : data.run.last_run
                ? t("pages.config.evolution_last_run", {
                    time: new Date(data.run.last_run).toLocaleString(),
                  })
                : null}
        </p>
      </div>
      {data.run.last_error && (
        <p className="text-destructive" role="alert">
          {data.run.last_error}
        </p>
      )}
      {mutation.error && (
        <p className="text-destructive" role="alert">
          {mutation.error.message}
        </p>
      )}
      {selected ? (
        <div className="flex min-w-0 flex-col gap-3">
          <div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setSelected("")
                setConfirmRollback(false)
                mutation.reset()
              }}
            >
              {t("pages.config.evolution_back")}
            </Button>
          </div>
          {review.isPending && <Skeleton className="h-24 w-full" />}
          {review.error && (
            <p className="text-destructive" role="alert">
              {review.error.message}
            </p>
          )}
          {detail && (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-medium break-all">
                  {detail.draft.target_skill_name}
                </h3>
                <Badge variant="outline">
                  {t(`pages.config.evolution_status_${detail.draft.status}`)}
                </Badge>
              </div>
              {detail.issue && <p role="alert">{detail.issue}</p>}
              {detail.shadowed_source && (
                <p className="text-warning">
                  {t(`pages.config.evolution_shadow_${detail.shadowed_source}`)}
                </p>
              )}
              {detail.draft.scan_findings?.map((finding) => (
                <p className="text-muted-foreground" key={finding}>
                  {finding}
                </p>
              ))}
              <pre
                aria-label={t("pages.config.evolution_diff")}
                tabIndex={0}
                className="bg-muted max-h-64 overflow-auto rounded-lg border p-3 font-mono text-xs"
              >
                {detail.diff}
              </pre>
              {detail.evidence.length > 0 && (
                <details>
                  <summary>{t("pages.config.evolution_evidence")}</summary>
                  <ul className="text-muted-foreground flex flex-col gap-2 py-2">
                    {detail.evidence.map((item, index) => (
                      <li className="flex flex-wrap gap-x-2" key={index}>
                        <span>
                          {t(
                            item.success == null
                              ? "pages.config.evolution_outcome_unknown"
                              : item.success
                                ? "pages.config.evolution_outcome_success"
                                : "pages.config.evolution_outcome_failure",
                          )}
                        </span>
                        <span>{item.summary}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              <div className="flex flex-wrap gap-2">
                {canRun && detail.can_refresh && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      mutation.mutate({ action: "refresh", draft_id: selected })
                    }
                  >
                    {t("pages.config.evolution_refresh_preview")}
                  </Button>
                )}
                {canRun &&
                  detail.draft.status === "candidate" &&
                  !detail.draft.published_version && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy}
                      onClick={() =>
                        mutation.mutate({
                          action: "reject",
                          draft_id: selected,
                        })
                      }
                    >
                      {t("pages.config.evolution_reject")}
                    </Button>
                  )}
                {data.mode === "apply" && detail.review_token && (
                  <Button
                    size="sm"
                    disabled={busy}
                    onClick={() => {
                      if (detail.review_token) {
                        mutation.mutate({
                          action: "publish",
                          draft_id: selected,
                          review_token: detail.review_token,
                        })
                      }
                    }}
                  >
                    {t("pages.config.evolution_publish")}
                  </Button>
                )}
                {detail.can_rollback && (
                  <Button
                    size="sm"
                    variant={confirmRollback ? "destructive" : "outline"}
                    disabled={busy}
                    onClick={() => {
                      if (confirmRollback) {
                        mutation.mutate({
                          action: "rollback",
                          draft_id: selected,
                        })
                      } else {
                        setConfirmRollback(true)
                      }
                    }}
                  >
                    {t(
                      confirmRollback
                        ? "pages.config.evolution_confirm_rollback"
                        : "pages.config.evolution_rollback",
                    )}
                  </Button>
                )}
              </div>
            </>
          )}
        </div>
      ) : (
        <>
          {data.drafts.length === 0 && (
            <p className="text-muted-foreground">
              {t("pages.config.evolution_empty")}
            </p>
          )}
          <ul className="flex flex-col gap-2">
            {data.drafts.map((draft) => (
              <li key={draft.id}>
                <Button
                  variant="outline"
                  className="h-auto min-h-8 w-full justify-between gap-3 py-2"
                  onClick={() => {
                    setSelected(draft.id)
                    mutation.reset()
                  }}
                >
                  <span className="min-w-0 truncate">
                    {draft.target_skill_name}
                  </span>
                  <Badge variant="outline">
                    {t(`pages.config.evolution_status_${draft.status}`)}
                  </Badge>
                </Button>
              </li>
            ))}
          </ul>
          {data.archive_suggestions.length > 0 && (
            <details>
              <summary>{t("pages.config.evolution_archive")}</summary>
              <ul className="text-muted-foreground flex flex-col gap-2 py-2">
                {data.archive_suggestions.map((skill) => (
                  <li key={skill}>{skill}</li>
                ))}
              </ul>
            </details>
          )}
        </>
      )}
    </div>
  )
}
