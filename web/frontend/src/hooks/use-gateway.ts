import { useAtomValue } from "jotai"
import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { restartGateway, startGateway, stopGateway } from "@/api/gateway"
import {
  beginGatewayStoppingTransition,
  cancelGatewayStoppingTransition,
  gatewayAtom,
  refreshGatewayState,
  subscribeGatewayPolling,
  updateGatewayStore,
} from "@/store"

export function useGateway() {
  const { t } = useTranslation()
  const gateway = useAtomValue(gatewayAtom)
  const { status: state, canStart, startReason, restartRequired } = gateway
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const describeStartReason = useCallback(
    (reason: string) => {
      if (reason === "no default model configured") {
        return t("header.gateway.startReason.noDefaultModel")
      }
      const model = reason.match(/^default model "(.+)"/)?.[1]
      if (model && reason.includes("has no credentials configured")) {
        return t("header.gateway.startReason.noCredentials", { model })
      }
      if (model && reason.includes("is not reachable")) {
        return t("header.gateway.startReason.notReachable", { model })
      }
      if (model && reason.includes("is invalid")) {
        return t("header.gateway.startReason.invalid", { model })
      }
      return reason
    },
    [t],
  )

  useEffect(() => {
    return subscribeGatewayPolling()
  }, [])

  const start = useCallback(async () => {
    if (!canStart) {
      toast.error(
        startReason
          ? describeStartReason(startReason)
          : t("header.gateway.startUnavailable"),
      )
      return
    }

    setError(null)
    setLoading(true)
    try {
      await startGateway()
      updateGatewayStore({
        status: "starting",
        restartRequired: false,
      })
    } catch (err) {
      console.error("Failed to start gateway:", err)
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
      toast.error(message)
    } finally {
      await refreshGatewayState({ force: true })
      setLoading(false)
    }
  }, [canStart, describeStartReason, startReason, t])

  const stop = useCallback(async () => {
    setError(null)
    setLoading(true)
    beginGatewayStoppingTransition()
    try {
      await stopGateway()
    } catch (err) {
      console.error("Failed to stop gateway:", err)
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
      toast.error(message)
      cancelGatewayStoppingTransition()
    } finally {
      await refreshGatewayState({ force: true })
      setLoading(false)
    }
  }, [])

  const restart = useCallback(async () => {
    if (state !== "running") return

    setError(null)
    setLoading(true)
    try {
      await restartGateway()
      updateGatewayStore({
        status: "restarting",
        restartRequired: false,
      })
    } catch (err) {
      console.error("Failed to restart gateway:", err)
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
      toast.error(message)
    } finally {
      await refreshGatewayState({ force: true })
      setLoading(false)
    }
  }, [state])

  return {
    state,
    loading,
    canStart,
    startReason: startReason ? describeStartReason(startReason) : undefined,
    restartRequired,
    start,
    stop,
    restart,
    error,
  }
}
