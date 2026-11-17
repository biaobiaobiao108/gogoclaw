import { IconCode, IconDeviceFloppy, IconTag } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { patchAppConfig, resetAppConfig } from "@/api/channels"
import { launcherFetch } from "@/api/http"
import { postLauncherDashboardSetup } from "@/api/launcher-auth"
import {
  getLauncherConfig,
  getSystemVersionInfo,
  setLauncherConfig as updateLauncherConfig,
} from "@/api/system"
import { ConfigChangeNotice } from "@/components/config-change-notice"
import { buildConfigPatch } from "@/components/config/config-save-patch"
import {
  AgentDefaultsSection,
  CronSection,
  DevicesSection,
  EvolutionSection,
  ExecSection,
  LauncherSection,
  MCPSection,
  RuntimeSection,
} from "@/components/config/config-sections"
import {
  buildFormFromConfig,
  type CoreConfigForm,
  EMPTY_FORM,
  EMPTY_LAUNCHER_FORM,
  type LauncherForm,
  type MCPServerForm,
  parseCIDRText,
  parseIntField,
  type TurnProfileForm,
} from "@/components/config/form-model"
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

export function ConfigPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<CoreConfigForm>(EMPTY_FORM)
  const [baseline, setBaseline] = useState<CoreConfigForm>(EMPTY_FORM)
  const [launcherForm, setLauncherForm] =
    useState<LauncherForm>(EMPTY_LAUNCHER_FORM)
  const [launcherBaseline, setLauncherBaseline] =
    useState<LauncherForm>(EMPTY_LAUNCHER_FORM)
  const [saving, setSaving] = useState(false)
  const [showFactoryResetDialog, setShowFactoryResetDialog] = useState(false)

  const { data, isLoading, error } = useQuery({
    queryKey: ["config"],
    queryFn: async () => {
      const controller = new AbortController()
      const timer = setTimeout(() => controller.abort(), 5000)
      try {
        const res = await launcherFetch("/api/config", {
          signal: controller.signal,
        })
        if (!res.ok) {
          throw new Error("Failed to load config")
        }
        return res.json()
      } finally {
        clearTimeout(timer)
      }
    },
  })

  const { data: launcherConfig, isLoading: isLauncherLoading } = useQuery({
    queryKey: ["system", "launcher-config"],
    queryFn: getLauncherConfig,
  })

  const { data: versionInfo } = useQuery({
    queryKey: ["system", "version"],
    queryFn: getSystemVersionInfo,
    staleTime: 5 * 60 * 1000,
  })

  useEffect(() => {
    if (!data) return
    const parsed = buildFormFromConfig(data)
    setForm(parsed)
    setBaseline(parsed)
  }, [data])

  useEffect(() => {
    if (!launcherConfig) return
    const parsed: LauncherForm = {
      port: String(launcherConfig.port),
      publicAccess: launcherConfig.public,
      allowedCIDRsText: (launcherConfig.allowed_cidrs ?? []).join("\n"),
      allowLocalhostBypass: launcherConfig.allow_localhost_bypass ?? true,
      dashboardCurrentPassword: "",
      dashboardPassword: "",
      dashboardPasswordConfirm: "",
    }
    setLauncherForm(parsed)
    setLauncherBaseline(parsed)
  }, [launcherConfig])

  const configDirty = JSON.stringify(form) !== JSON.stringify(baseline)
  const launcherSettingsDirty =
    launcherForm.port !== launcherBaseline.port ||
    launcherForm.publicAccess !== launcherBaseline.publicAccess ||
    launcherForm.allowedCIDRsText !== launcherBaseline.allowedCIDRsText ||
    launcherForm.allowLocalhostBypass !== launcherBaseline.allowLocalhostBypass
  const launcherPasswordDirty =
    launcherForm.dashboardCurrentPassword.trim() !== "" ||
    launcherForm.dashboardPassword.trim() !== "" ||
    launcherForm.dashboardPasswordConfirm.trim() !== ""
  const launcherDirty = launcherSettingsDirty || launcherPasswordDirty
  const isDirty = configDirty || launcherDirty

  const updateField = <K extends keyof CoreConfigForm>(
    key: K,
    value: CoreConfigForm[K],
  ) => {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const updateLauncherField = <K extends keyof LauncherForm>(
    key: K,
    value: LauncherForm[K],
  ) => {
    setLauncherForm((prev) => ({ ...prev, [key]: value }))
  }

  const handleMCPServerAdd = () => {
    const nextIndex = form.mcpServers.length + 1
    const server: MCPServerForm = {
      id: `mcp-${Date.now()}-${nextIndex}`,
      name: "",
      enabled: true,
      type: "stdio",
      url: "",
      command: "",
      argsText: "",
      envText: "{}",
      envFile: "",
      headersText: "{}",
    }
    updateField("mcpServers", [...form.mcpServers, server])
  }

  const handleMCPServerRemove = (id: string) => {
    updateField(
      "mcpServers",
      form.mcpServers.filter((server) => server.id !== id),
    )
  }

  const handleMCPServerFieldChange = <K extends keyof MCPServerForm>(
    id: string,
    key: K,
    value: MCPServerForm[K],
  ) => {
    updateField(
      "mcpServers",
      form.mcpServers.map((server) =>
        server.id === id ? { ...server, [key]: value } : server,
      ),
    )
  }

  const handleTurnProfileFieldChange = <K extends keyof TurnProfileForm>(
    key: K,
    value: TurnProfileForm[K],
  ) => {
    updateField("turnProfile", { ...form.turnProfile, [key]: value })
  }

  const handleReset = () => {
    setForm(baseline)
    setLauncherForm(launcherBaseline)
    toast.info(t("pages.config.reset_success"))
  }

  const handleFactoryReset = async () => {
    try {
      await resetAppConfig()
      const fresh = await launcherFetch("/api/config").then((r) => r.json())
      const parsed = buildFormFromConfig(fresh)
      setForm(parsed)
      setBaseline(parsed)
      await queryClient.invalidateQueries({ queryKey: ["config"] })
      await refreshGatewayState()
      toast.success(t("pages.config.factory_reset_success"))
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : t("pages.config.factory_reset_error"),
      )
    } finally {
      setShowFactoryResetDialog(false)
    }
  }

  const handleSave = async () => {
    try {
      setSaving(true)
      const currentPassword = launcherForm.dashboardCurrentPassword.trim()
      const password = launcherForm.dashboardPassword.trim()
      const confirm = launcherForm.dashboardPasswordConfirm.trim()
      if (launcherPasswordDirty) {
        if (!currentPassword) {
          throw new Error(t("pages.config.dashboard_current_password_required"))
        }
        if (!password) {
          throw new Error(t("pages.config.dashboard_password_required"))
        }
        if (password !== confirm) {
          throw new Error(t("pages.config.dashboard_password_mismatch"))
        }
        if (Array.from(password).length < 8) {
          throw new Error(t("pages.config.dashboard_password_min_length"))
        }
      }

      if (configDirty) {
        const patch = buildConfigPatch(form, baseline)
        await patchAppConfig(patch)

        setBaseline(form)
        queryClient.invalidateQueries({ queryKey: ["config"] })
      }

      let savedLauncherForm: LauncherForm | null = null
      if (launcherSettingsDirty) {
        const port = parseIntField(launcherForm.port, "Service port", {
          min: 1,
          max: 65535,
        })
        const allowedCIDRs = parseCIDRText(launcherForm.allowedCIDRsText)
        const savedLauncherConfig = await updateLauncherConfig({
          port,
          public: launcherForm.publicAccess,
          allowed_cidrs: allowedCIDRs,
          allow_localhost_bypass: launcherForm.allowLocalhostBypass,
        })
        const parsedLauncher: LauncherForm = {
          port: String(savedLauncherConfig.port),
          publicAccess: savedLauncherConfig.public,
          allowedCIDRsText: (savedLauncherConfig.allowed_cidrs ?? []).join(
            "\n",
          ),
          allowLocalhostBypass:
            savedLauncherConfig.allow_localhost_bypass ?? true,
          dashboardCurrentPassword: "",
          dashboardPassword: "",
          dashboardPasswordConfirm: "",
        }
        savedLauncherForm = parsedLauncher
        setLauncherForm(parsedLauncher)
        setLauncherBaseline(parsedLauncher)
        queryClient.setQueryData(
          ["system", "launcher-config"],
          savedLauncherConfig,
        )
      }

      if (launcherPasswordDirty) {
        const result = await postLauncherDashboardSetup(
          password,
          confirm,
          currentPassword,
        )
        if (!result.ok) {
          throw new Error(result.error)
        }

        const clearedLauncherForm = savedLauncherForm ?? {
          ...launcherForm,
          dashboardCurrentPassword: "",
          dashboardPassword: "",
          dashboardPasswordConfirm: "",
        }
        setLauncherForm(clearedLauncherForm)
        if (savedLauncherForm) {
          setLauncherBaseline(savedLauncherForm)
        }
      }

      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("pages.config.save_success"),
        t("navigation.config"),
        gateway?.restartRequired === true,
      )
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : t("pages.config.save_error"),
      )
    } finally {
      setSaving(false)
    }
  }

  const factoryResetButton = (
    <AlertDialog
      open={showFactoryResetDialog}
      onOpenChange={setShowFactoryResetDialog}
    >
      <AlertDialogTrigger asChild>
        <Button variant="destructive" disabled={saving}>
          {t("pages.config.factory_reset")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("pages.config.factory_reset_confirm_title")}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("pages.config.factory_reset_confirm_desc")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction onClick={handleFactoryReset}>
            {t("pages.config.factory_reset_confirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )

  const actionButtons = (
    <div className="flex justify-end gap-2">
      {factoryResetButton}
      <Button
        variant="outline"
        onClick={handleReset}
        disabled={!isDirty || saving}
      >
        {t("common.reset")}
      </Button>
      <Button onClick={handleSave} disabled={!isDirty || saving}>
        <IconDeviceFloppy className="size-4" />
        {saving ? t("common.saving") : t("common.save")}
      </Button>
    </div>
  )

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.config")}
        titleExtra={
          versionInfo && (
            <Badge
              variant="secondary"
              className="gap-1 font-mono text-[11px] font-normal opacity-80"
            >
              <IconTag className="size-3 opacity-70" />
              {versionInfo.version}
            </Badge>
          )
        }
        children={
          <Button variant="outline" asChild>
            <Link to="/config/raw">
              <IconCode className="size-4" />
              {t("pages.config.open_raw")}
            </Link>
          </Button>
        }
      />
      <div className="min-h-0 flex-1 overflow-auto p-4 sm:p-6">
        <div className="mx-auto w-full max-w-4xl space-y-6">
          {isLoading ? (
            <div className="text-muted-foreground py-6 text-sm">
              {t("labels.loading")}
            </div>
          ) : error ? (
            <div className="space-y-4">
              <div className="text-destructive py-6 text-sm">
                {t("pages.config.load_error")}
              </div>
              <div className="flex justify-end">{factoryResetButton}</div>
            </div>
          ) : (
            <div className="space-y-6">
              <LauncherSection
                launcherForm={launcherForm}
                onFieldChange={updateLauncherField}
                disabled={saving || isLauncherLoading}
              />

              <AgentDefaultsSection
                form={form}
                onFieldChange={updateField}
                onTurnProfileFieldChange={handleTurnProfileFieldChange}
              />

              <RuntimeSection form={form} onFieldChange={updateField} />

              <EvolutionSection form={form} onFieldChange={updateField} />

              <MCPSection
                form={form}
                onFieldChange={updateField}
                onAddServer={handleMCPServerAdd}
                onRemoveServer={handleMCPServerRemove}
                onServerFieldChange={handleMCPServerFieldChange}
              />

              <ExecSection form={form} onFieldChange={updateField} />

              <CronSection form={form} onFieldChange={updateField} />

              <DevicesSection form={form} onFieldChange={updateField} />

              {!isDirty && actionButtons}
            </div>
          )}
        </div>
      </div>
      {isDirty && (
        <div className="border-border/70 bg-background/95 supports-backdrop-filter:bg-background/80 shrink-0 border-t px-3 py-3 backdrop-blur lg:px-6">
          <div className="mx-auto flex w-full max-w-4xl flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex-1">
              <ConfigChangeNotice
                kind="save"
                title={t("common.saveChangesTitle")}
              />
            </div>
            {actionButtons}
          </div>
        </div>
      )}
    </div>
  )
}
