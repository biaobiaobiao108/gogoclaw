import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import { getGatewayReadiness } from "@/api/gateway"
import { Button } from "@/components/ui/button"
import { useGateway } from "@/hooks/use-gateway"

const failurePrefix = "configuration reload failed: "

export function GatewayConfigStatus() {
  const { t } = useTranslation()
  const { state, loading, restart, restartRequired, error } = useGateway()
  const { data } = useQuery({
    queryKey: ["gateway", "readiness"],
    queryFn: ({ signal }) => getGatewayReadiness(signal),
    enabled: state === "running",
    refetchInterval: 3000,
    retry: false,
  })
  const reload = data?.checks?.reload
  const failure = reload?.message?.startsWith(failurePrefix)
    ? reload.message.slice(failurePrefix.length)
    : undefined
  const runtimeState =
    state !== "running"
      ? state
      : failure
        ? "failed"
        : reload?.status === "fail"
          ? "applying"
          : data?.status === "unavailable"
            ? "unknown"
            : data?.config_state === "applied"
              ? "applied"
              : data?.config_state === "pending" || restartRequired
                ? "pending"
                : "unknown"

  return (
    <section
      aria-label={t("recovery.config.title")}
      className="border-border flex flex-wrap items-center justify-between gap-3 border-b pb-4"
    >
      <div className="min-w-0">
        <h2 className="text-sm font-medium">{t("recovery.config.title")}</h2>
        <p className="text-muted-foreground mt-1 text-xs" role="status">
          {t(`recovery.config.${runtimeState}`)}
          {runtimeState === "failed" && reload && (
            <>
              {" "}
              ·{" "}
              {t(`recovery.config.components.${failure}`, {
                defaultValue: t("recovery.config.components.runtime services"),
              })}{" "}
              · {new Date(reload.timestamp).toLocaleString()}
            </>
          )}
        </p>
        {error && (
          <p className="text-destructive mt-1 text-xs" role="alert">
            {error}
          </p>
        )}
      </div>
      {(runtimeState === "failed" ||
        runtimeState === "pending" ||
        runtimeState === "error") && (
        <div className="flex flex-wrap gap-2">
          {(runtimeState === "failed" || runtimeState === "error") && (
            <Button asChild size="sm" variant="ghost">
              <Link to="/logs">{t("navigation.logs")}</Link>
            </Button>
          )}
          {state === "running" && (
            <Button
              size="sm"
              variant="outline"
              disabled={loading}
              onClick={() => void restart()}
            >
              {t("recovery.config.reapply")}
            </Button>
          )}
        </div>
      )}
    </section>
  )
}
