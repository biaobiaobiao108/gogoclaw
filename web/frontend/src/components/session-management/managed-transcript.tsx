import { useInfiniteQuery } from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useCallback, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  getManagedTranscript,
  type ManagedSession,
} from "@/api/session-management"
import { Button } from "@/components/ui/button"

export function ManagedTranscript({
  selected,
  onDelete,
  pending,
}: {
  selected: ManagedSession
  onDelete: () => void
  pending: boolean
}) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [expanded, setExpanded] = useState(() => new Set<number>())
  const detail = useInfiniteQuery({
    queryKey: ["managed-transcript", selected.id],
    queryFn: ({ pageParam, signal }) =>
      getManagedTranscript(selected.id, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: (page) => (page.has_more ? page.next_cursor : undefined),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  })
  const pages = detail.data?.pages
  const messages = useMemo(
    () =>
      pages
        ?.slice()
        .reverse()
        .flatMap((page) => page.messages ?? []) ?? [],
    [pages],
  )
  const deliveries = pages?.[0]?.deliveries ?? []
  const deliveryRows = deliveries.length > 0 ? 1 : 0
  // Older pages only prepend, so numbering from the newest message keeps keys stable.
  const getItemKey = useCallback(
    (index: number) =>
      index < deliveryRows
        ? "deliveries"
        : messages.length - index + deliveryRows,
    [deliveryRows, messages.length],
  )
  const virtualizer = useVirtualizer({
    count: messages.length + deliveryRows,
    getScrollElement: () => scrollRef.current,
    getItemKey,
    estimateSize: () => 80,
    gap: 16,
    overscan: 6,
    anchorTo: "end",
  })

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b p-4">
        <div className="min-w-0 flex-1 basis-full sm:basis-0">
          <div className="truncate text-sm font-medium">
            {selected.scope.channel || t("sessionManagement.unknown")} ·{" "}
            {selected.scope.agent_id || "—"}
          </div>
          <div className="text-muted-foreground truncate font-mono text-xs">
            {selected.key}
          </div>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {detail.hasNextPage && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => void detail.fetchNextPage()}
              disabled={detail.isFetching}
            >
              {t("sessionManagement.older")}
            </Button>
          )}
          <Button
            variant="destructive"
            size="sm"
            onClick={onDelete}
            disabled={pending}
          >
            {t("sessionManagement.delete")}
          </Button>
        </div>
      </div>

      {detail.error && (
        <div
          role="alert"
          className="text-destructive flex items-center gap-2 px-4 py-2 text-sm"
        >
          {detail.error.message}
          {!detail.data && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void detail.refetch()}
            >
              {t("sessionManagement.retry")}
            </Button>
          )}
        </div>
      )}
      <div
        ref={scrollRef}
        className="min-h-0 flex-1 overflow-auto p-4 [overflow-anchor:none]"
        aria-busy={detail.isFetching}
      >
        {detail.isPending && (
          <p role="status" className="text-muted-foreground text-sm">
            {t("sessionManagement.loading")}
          </p>
        )}
        <div
          className="relative mx-auto max-w-4xl"
          style={{ height: virtualizer.getTotalSize() }}
        >
          {virtualizer.getVirtualItems().map((row) => {
            const message = messages[row.index - deliveryRows]
            const key = Number(row.key)
            return (
              <div
                key={row.key}
                data-index={row.index}
                ref={virtualizer.measureElement}
                className="absolute top-0 w-full"
                style={{ transform: `translateY(${row.start}px)` }}
              >
                {message ? (
                  <article className="border-b pb-4">
                    <div className="text-muted-foreground mb-2 text-xs">
                      {message.role}
                      {message.created_at
                        ? ` · ${new Date(message.created_at).toLocaleString()}`
                        : ""}
                    </div>
                    {message.content && (
                      <div className="text-sm break-words whitespace-pre-wrap">
                        {message.content}
                      </div>
                    )}
                    {message.reasoning_content && (
                      <details
                        className="mt-2 text-sm"
                        open={expanded.has(key)}
                      >
                        <summary
                          onClick={(event) => {
                            event.preventDefault()
                            setExpanded((current) => {
                              const next = new Set(current)
                              if (next.has(key)) next.delete(key)
                              else next.add(key)
                              return next
                            })
                          }}
                        >
                          {t("sessionManagement.reasoning")}
                        </summary>
                        <div className="break-words whitespace-pre-wrap">
                          {message.reasoning_content}
                        </div>
                      </details>
                    )}
                    {message.tool_calls?.map((call, i) => (
                      <div key={call.id || i} className="mt-2 text-sm">
                        <div className="font-mono">
                          {call.function?.name ||
                            t("sessionManagement.toolCall")}
                        </div>
                        {call.function?.arguments && (
                          <pre className="overflow-auto break-words whitespace-pre-wrap">
                            {call.function.arguments}
                          </pre>
                        )}
                      </div>
                    ))}
                    {message.attachments?.map((attachment, i) => (
                      <div key={i} className="mt-2 text-sm break-all">
                        {attachment.url &&
                        /^(https?:\/\/|\/(?!\/)|data:image\/)/i.test(
                          attachment.url,
                        ) ? (
                          <a
                            className="underline"
                            href={attachment.url}
                            target="_blank"
                            rel="noreferrer"
                          >
                            {attachment.filename ||
                              attachment.type ||
                              t("sessionManagement.attachment")}
                          </a>
                        ) : (
                          attachment.filename ||
                          attachment.ref ||
                          attachment.type ||
                          t("sessionManagement.attachment")
                        )}
                      </div>
                    ))}
                    {message.media?.map((ref, i) => (
                      <div key={i} className="mt-2 text-sm break-all">
                        {t("sessionManagement.media")}: {ref}
                      </div>
                    ))}
                    {!message.content &&
                      !message.reasoning_content &&
                      !message.tool_calls?.length &&
                      !message.attachments?.length &&
                      !message.media?.length &&
                      "—"}
                  </article>
                ) : (
                  <section className="border-b pb-4">
                    <h3 className="text-sm font-medium">
                      {t("sessionManagement.deliveries")}
                    </h3>
                    <ul className="mt-2 space-y-1">
                      {deliveries.map((delivery) => (
                        <li key={delivery.id} className="text-sm">
                          <div className="flex flex-wrap items-center justify-between gap-x-3">
                            <span>
                              {delivery.channel} ·{" "}
                              {t(
                                `sessionManagement.deliveryStatus.${delivery.status}`,
                              )}
                            </span>
                            <time
                              className="text-muted-foreground text-xs"
                              dateTime={delivery.updated_at}
                            >
                              {new Date(delivery.updated_at).toLocaleString()}
                            </time>
                          </div>
                          {delivery.preview && (
                            <p className="text-foreground text-xs break-words">
                              {delivery.preview}
                            </p>
                          )}
                          <p className="text-muted-foreground text-xs break-all">
                            {delivery.message_id
                              ? t("recovery.delivery.message", {
                                  id: delivery.message_id,
                                })
                              : t("recovery.delivery.record", {
                                  id: delivery.id,
                                })}
                          </p>
                          <p className="text-muted-foreground text-xs">
                            {delivery.status === "delivery_unknown" ||
                            delivery.status === "dispatching" ||
                            delivery.status === "streaming"
                              ? t("recovery.delivery.check")
                              : delivery.next_attempt_at
                                ? t("recovery.delivery.next", {
                                    time: new Date(
                                      delivery.next_attempt_at,
                                    ).toLocaleString(),
                                  })
                                : delivery.status === "dead_letter" ||
                                    delivery.status === "failed"
                                  ? t("recovery.delivery.exhausted")
                                  : null}
                          </p>
                        </li>
                      ))}
                    </ul>
                  </section>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </>
  )
}
