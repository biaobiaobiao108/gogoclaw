import {
  type InfiniteData,
  useInfiniteQuery,
  useQueryClient,
} from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  deleteManagedSession,
  getManagedSessions,
  type ManagedSession,
  type ManagedSessionPage,
} from "@/api/session-management"
import { PageHeader } from "@/components/page-header"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

import { ManagedTranscript } from "./managed-transcript"

export function SessionsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<ManagedSession | null>(null)
  const [detailRefresh, setDetailRefresh] = useState(0)
  const [query, setQuery] = useState("")
  const [search, setSearch] = useState("")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const waitingForSearch = query.trim() !== search

  useEffect(() => {
    const timer = setTimeout(() => setSearch(query.trim()), 250)
    return () => clearTimeout(timer)
  }, [query])

  const list = useInfiniteQuery({
    queryKey: ["managed-sessions", search],
    queryFn: ({ pageParam, signal }) =>
      getManagedSessions(search, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  })
  const pages = list.data?.pages
  const items = useMemo(() => {
    const unique = new Map<string, ManagedSession>()
    for (const page of pages ?? []) {
      for (const item of page.items) unique.set(item.id, item)
    }
    return [...unique.values()]
  }, [pages])
  const getItemKey = useCallback((index: number) => items[index].id, [items])
  const virtualizer = useVirtualizer({
    count: waitingForSearch ? 0 : items.length,
    getScrollElement: () => listRef.current,
    getItemKey,
    estimateSize: () => 80,
    overscan: 6,
  })

  const remove = async () => {
    if (!selected) return
    setPending(true)
    setError("")
    try {
      await deleteManagedSession(selected.id)
      setSelected((current) => (current?.id === selected.id ? null : current))
      const lists = { queryKey: ["managed-sessions"] }
      await queryClient.cancelQueries(lists)
      queryClient.setQueriesData<InfiniteData<ManagedSessionPage>>(
        lists,
        (data) =>
          data && {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              items: page.items.filter((item) => item.id !== selected.id),
            })),
          },
      )
      void queryClient.resetQueries({
        ...lists,
        predicate: (query) => query.state.data === undefined,
      })
    } catch (e) {
      setError(
        String(e).includes("stop gateway before deleting sessions")
          ? t("sessionManagement.stopGateway")
          : String(e),
      )
    } finally {
      setPending(false)
      setConfirm(false)
    }
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title={t("sessionManagement.title")}>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setError("")
            void queryClient.resetQueries({ queryKey: ["managed-sessions"] })
            if (selected) {
              void queryClient.resetQueries({
                queryKey: ["managed-transcript", selected.id],
              })
              setDetailRefresh((current) => current + 1)
            }
          }}
        >
          {t("sessionManagement.refresh")}
        </Button>
      </PageHeader>
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <section className="flex min-h-0 flex-col border-b md:w-96 md:shrink-0 md:border-r md:border-b-0">
          <div className="p-4">
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("sessionManagement.search")}
              aria-label={t("sessionManagement.search")}
            />
          </div>
          <div
            ref={listRef}
            className="max-h-64 overflow-auto px-4 pb-4 md:max-h-none md:flex-1"
            aria-busy={waitingForSearch || list.isFetching}
          >
            {waitingForSearch || list.isPending ? (
              <p role="status" className="text-muted-foreground text-sm">
                {t("sessionManagement.loading")}
              </p>
            ) : (
              <>
                {items.length === 0 && !list.hasNextPage && !list.error && (
                  <p className="text-muted-foreground text-sm">
                    {t("sessionManagement.empty")}
                  </p>
                )}
                <div
                  className="relative"
                  style={{ height: virtualizer.getTotalSize() }}
                >
                  {virtualizer.getVirtualItems().map((row) => {
                    const item = items[row.index]
                    return (
                      <div
                        key={row.key}
                        ref={virtualizer.measureElement}
                        data-index={row.index}
                        className="absolute top-0 w-full"
                        style={{ transform: `translateY(${row.start}px)` }}
                      >
                        <button
                          onClick={() => {
                            setError("")
                            setSelected(item)
                          }}
                          aria-pressed={selected?.id === item.id}
                          className={`hover:bg-muted w-full rounded-md p-3 text-left text-sm ${selected?.id === item.id ? "bg-muted" : ""}`}
                        >
                          <div className="truncate font-medium">
                            {item.scope.channel ||
                              t("sessionManagement.unknown")}{" "}
                            · {item.scope.agent_id || "—"}
                          </div>
                          <div className="text-muted-foreground truncate text-xs">
                            {Object.values(item.scope.values ?? {}).join(
                              " · ",
                            ) || item.key}
                          </div>
                          <div className="text-muted-foreground mt-1 text-xs">
                            {new Date(item.updated).toLocaleString()} ·{" "}
                            {t("sessionManagement.messages", {
                              count: item.count,
                            })}
                          </div>
                        </button>
                      </div>
                    )
                  })}
                </div>
                {list.error && (
                  <p role="alert" className="text-destructive text-sm">
                    {list.error.message}
                  </p>
                )}
                {(list.hasNextPage || list.error) && (
                  <Button
                    className="mt-2"
                    variant="outline"
                    size="sm"
                    disabled={list.isFetching}
                    onClick={() =>
                      void (list.hasNextPage
                        ? list.fetchNextPage()
                        : list.refetch())
                    }
                  >
                    {t(
                      list.error
                        ? "sessionManagement.retry"
                        : "sessionManagement.more",
                    )}
                  </Button>
                )}
              </>
            )}
          </div>
        </section>
        <section className="flex min-h-0 min-w-0 flex-1 flex-col">
          {error && (
            <div role="alert" className="text-destructive px-4 py-2 text-sm">
              {error}
            </div>
          )}
          {!selected ? (
            <p className="text-muted-foreground p-4 text-sm">
              {t("sessionManagement.select")}
            </p>
          ) : (
            <ManagedTranscript
              key={selected.id + ":" + detailRefresh}
              selected={selected}
              onDelete={() => setConfirm(true)}
              pending={pending}
            />
          )}
        </section>
      </div>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("sessionManagement.deleteTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("sessionManagement.deleteDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t("sessionManagement.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault()
                void remove()
              }}
              disabled={pending}
            >
              {t("sessionManagement.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
