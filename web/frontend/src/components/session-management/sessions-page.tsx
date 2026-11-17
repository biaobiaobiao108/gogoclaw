import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { launcherFetch } from "@/api/http"
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

type Item = {
  id: string
  key: string
  count: number
  updated: string
  scope: {
    channel?: string
    agent_id?: string
    account?: string
    values?: Record<string, string>
  }
}
type Message = {
  role: string
  content: string
  created_at?: string
  reasoning_content?: string
  media?: string[]
  attachments?: {
    type?: string
    url?: string
    ref?: string
    filename?: string
  }[]
  tool_calls?: {
    id?: string
    function?: { name?: string; arguments?: string }
  }[]
}
type Page = {
  messages: Message[] | null
  next_cursor: number
  has_more: boolean
}
async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await launcherFetch(url, init)
  if (!response.ok) throw new Error((await response.text()).trim())
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}
export function SessionsPage() {
  const { t } = useTranslation()
  const [items, setItems] = useState<Item[]>([])
  const [selected, setSelected] = useState<Item | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [cursor, setCursor] = useState(0)
  const [more, setMore] = useState(false)
  const [query, setQuery] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const selectionVersion = useRef(0)
  const select = (item: Item | null) => {
    selectionVersion.current++
    setPending(false)
    setSelected(item)
  }
  const load = useCallback(async () => {
    setLoading(true)
    setError("")
    try {
      setItems(await request<Item[]>("/api/session-management"))
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])
  useEffect(() => {
    if (!selected) return
    let cancelled = false
    setMessages([])
    setMore(false)
    setError("")
    void request<Page>(`/api/session-management/${selected.id}`)
      .then((page) => {
        if (!cancelled) {
          setMessages(page.messages ?? [])
          setCursor(page.next_cursor)
          setMore(page.has_more)
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(String(e))
      })
    return () => {
      cancelled = true
    }
  }, [selected])
  const older = async () => {
    if (!selected) return
    const version = selectionVersion.current
    setPending(true)
    try {
      const page = await request<Page>(
        `/api/session-management/${selected.id}?before=${cursor}`,
      )
      if (version !== selectionVersion.current) return
      setMessages((current) => [...(page.messages ?? []), ...current])
      setCursor(page.next_cursor)
      setMore(page.has_more)
    } catch (e) {
      if (version === selectionVersion.current) setError(String(e))
    } finally {
      if (version === selectionVersion.current) setPending(false)
    }
  }
  const remove = async () => {
    if (!selected) return
    const version = selectionVersion.current
    setPending(true)
    setError("")
    try {
      await request(`/api/session-management/${selected.id}`, {
        method: "DELETE",
      })
      setItems((current) => current.filter((item) => item.id !== selected.id))
      if (version === selectionVersion.current) select(null)
    } catch (e) {
      if (version !== selectionVersion.current) return
      setError(
        String(e).includes("stop gateway before deleting sessions")
          ? t("sessionManagement.stopGateway")
          : String(e),
      )
    } finally {
      if (version === selectionVersion.current) setPending(false)
      setConfirm(false)
    }
  }
  const filtered = items.filter((item) =>
    JSON.stringify([item.scope, item.key])
      .toLowerCase()
      .includes(query.toLowerCase()),
  )
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title={t("sessionManagement.title")}>
        <Button variant="ghost" size="sm" onClick={() => void load()}>
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
          <div className="max-h-64 overflow-auto px-4 pb-4 md:max-h-none md:flex-1">
            {loading ? (
              <p className="text-muted-foreground text-sm">
                {t("sessionManagement.loading")}
              </p>
            ) : filtered.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {t("sessionManagement.empty")}
              </p>
            ) : (
              filtered.map((item) => (
                <button
                  key={item.id}
                  onClick={() => select(item)}
                  className={`hover:bg-muted w-full rounded-md p-3 text-left text-sm ${selected?.id === item.id ? "bg-muted" : ""}`}
                >
                  <div className="truncate font-medium">
                    {item.scope.channel || t("sessionManagement.unknown")} ·{" "}
                    {item.scope.agent_id || "—"}
                  </div>
                  <div className="text-muted-foreground truncate text-xs">
                    {Object.values(item.scope.values ?? {}).join(" · ") ||
                      item.key}
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    {new Date(item.updated).toLocaleString()} ·{" "}
                    {t("sessionManagement.messages", { count: item.count })}
                  </div>
                </button>
              ))
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
            <>
              <div className="flex items-center justify-between gap-4 border-b p-4">
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">
                    {selected.scope.channel || t("sessionManagement.unknown")} ·{" "}
                    {selected.scope.agent_id || "—"}
                  </div>
                  <div className="text-muted-foreground truncate font-mono text-xs">
                    {selected.key}
                  </div>
                </div>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => setConfirm(true)}
                  disabled={pending}
                >
                  {t("sessionManagement.delete")}
                </Button>
              </div>
              <div className="flex-1 overflow-auto p-4">
                <div className="mx-auto max-w-4xl space-y-4">
                  {more && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void older()}
                      disabled={pending}
                    >
                      {t("sessionManagement.older")}
                    </Button>
                  )}
                  {messages.map((message, index) => (
                    <article key={index} className="border-b pb-4">
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
                        <details className="mt-2 text-sm">
                          <summary>{t("sessionManagement.reasoning")}</summary>
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
                  ))}
                </div>
              </div>
            </>
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
