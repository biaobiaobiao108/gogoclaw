import { useTranslation } from "react-i18next"

import type { LauncherForm } from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

import {
  ConfigSectionCard,
  type UpdateLauncherField,
} from "./config-section-card"

interface LauncherSectionProps {
  launcherForm: LauncherForm
  onFieldChange: UpdateLauncherField
  disabled: boolean
}

export function LauncherSection({
  launcherForm,
  onFieldChange,
  disabled,
}: LauncherSectionProps) {
  const { t } = useTranslation()

  return (
    <ConfigSectionCard
      title={t("pages.config.sections.launcher")}
      description={t("pages.config.launcher_section_hint")}
    >
      <Field
        label={t("pages.config.dashboard_current_password")}
        hint={t("pages.config.dashboard_current_password_hint")}
        layout="setting-row"
        controlClassName="md:max-w-md"
      >
        <Input
          type="password"
          value={launcherForm.dashboardCurrentPassword}
          disabled={disabled}
          autoComplete="current-password"
          placeholder={t("pages.config.dashboard_current_password_placeholder")}
          onChange={(e) =>
            onFieldChange("dashboardCurrentPassword", e.target.value)
          }
        />
      </Field>

      <Field
        label={t("pages.config.dashboard_password")}
        hint={t("pages.config.dashboard_password_hint")}
        layout="setting-row"
        controlClassName="md:max-w-md"
      >
        <Input
          type="password"
          value={launcherForm.dashboardPassword}
          disabled={disabled}
          autoComplete="new-password"
          placeholder={t("pages.config.dashboard_password_placeholder")}
          onChange={(e) => onFieldChange("dashboardPassword", e.target.value)}
        />
      </Field>

      {launcherForm.dashboardPassword.trim() !== "" && (
        <Field
          label={t("pages.config.dashboard_password_confirm")}
          hint={t("pages.config.dashboard_password_confirm_hint")}
          layout="setting-row"
          controlClassName="md:max-w-md"
        >
          <Input
            type="password"
            value={launcherForm.dashboardPasswordConfirm}
            disabled={disabled}
            autoComplete="new-password"
            placeholder={t(
              "pages.config.dashboard_password_confirm_placeholder",
            )}
            onChange={(e) =>
              onFieldChange("dashboardPasswordConfirm", e.target.value)
            }
          />
        </Field>
      )}

      <SwitchCardField
        label={t("pages.config.lan_access")}
        hint={t("pages.config.lan_access_hint")}
        layout="setting-row"
        checked={launcherForm.publicAccess}
        disabled={disabled}
        onCheckedChange={(checked) => onFieldChange("publicAccess", checked)}
      />

      <Field
        label={t("pages.config.server_port")}
        hint={t("pages.config.server_port_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          max={65535}
          value={launcherForm.port}
          disabled={disabled}
          onChange={(e) => onFieldChange("port", e.target.value)}
        />
      </Field>

      <Field
        label={t("pages.config.allowed_cidrs")}
        hint={t("pages.config.allowed_cidrs_hint")}
        layout="setting-row"
        controlClassName="md:max-w-md"
      >
        <Textarea
          value={launcherForm.allowedCIDRsText}
          disabled={disabled}
          placeholder={t("pages.config.allowed_cidrs_placeholder")}
          className="min-h-[88px]"
          onChange={(e) => onFieldChange("allowedCIDRsText", e.target.value)}
        />
      </Field>

      <SwitchCardField
        label={t("pages.config.allow_localhost_bypass")}
        hint={t("pages.config.allow_localhost_bypass_hint")}
        layout="setting-row"
        checked={launcherForm.allowLocalhostBypass}
        disabled={disabled}
        onCheckedChange={(checked) =>
          onFieldChange("allowLocalhostBypass", checked)
        }
      />
    </ConfigSectionCard>
  )
}
