import { IconShieldExclamation } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { useAtomValue } from "jotai"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { gatewayAtom } from "@/store"

export function PublicExposureBanner() {
  const { t } = useTranslation()
  const exposure = useAtomValue(gatewayAtom).publicExposure
  const [open, setOpen] = useState(false)
  if (!exposure?.public) return null

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <div
        role="status"
        className="border-warning/30 bg-warning/10 flex min-h-9 shrink-0 items-center justify-between gap-3 border-b px-4 py-1.5"
      >
        <div className="flex min-w-0 items-center gap-2">
          <IconShieldExclamation className="text-warning size-4 shrink-0" />
          <span className="truncate text-xs font-medium">
            {t("security.exposure.banner")}
          </span>
        </div>
        <DialogTrigger asChild>
          <Button
            size="sm"
            variant="outline"
            className="h-7 shrink-0 px-2.5 text-xs"
          >
            {t("security.exposure.review")}
          </Button>
        </DialogTrigger>
      </div>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("security.exposure.title")}</DialogTitle>
          <DialogDescription>
            {t("security.exposure.description")}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3 text-sm">
          {exposure.launcher || exposure.public_url ? (
            <p>{t("security.exposure.launcher")}</p>
          ) : null}
          {exposure.public_channels?.length ? (
            <p>
              {t("security.exposure.channels", {
                channels: exposure.public_channels.join(", "),
              })}
            </p>
          ) : null}
          {exposure.unapproved_high_risk_tools?.length ? (
            <p>
              {t("security.exposure.unapprovedTools", {
                tools: exposure.unapproved_high_risk_tools.join(", "),
              })}
            </p>
          ) : null}
          {exposure.exec_guard_disabled ? (
            <p className="text-destructive">
              {t("security.exposure.execGuardDisabled")}
            </p>
          ) : null}
          <p className="text-muted-foreground">
            {t("security.exposure.approvalHint")}
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" asChild>
            <Link to="/config" onClick={() => setOpen(false)}>
              {t("security.exposure.openConfig")}
            </Link>
          </Button>
          <Button onClick={() => setOpen(false)}>{t("common.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
