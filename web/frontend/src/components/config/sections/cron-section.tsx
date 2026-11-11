import { useTranslation } from "react-i18next"

import type { CoreConfigForm } from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Input } from "@/components/ui/input"

import { ConfigSectionCard, type UpdateCoreField } from "./config-section-card"

interface CronSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
}

export function CronSection({ form, onFieldChange }: CronSectionProps) {
  const { t } = useTranslation()

  return (
    <ConfigSectionCard title={t("pages.config.sections.cron")}>
      <SwitchCardField
        label={t("pages.config.allow_shell_execution")}
        hint={t("pages.config.allow_shell_execution_hint")}
        layout="setting-row"
        checked={form.allowCommand}
        disabled={!form.execEnabled}
        onCheckedChange={(checked) => onFieldChange("allowCommand", checked)}
      />

      <Field
        label={t("pages.config.cron_exec_timeout")}
        hint={t("pages.config.cron_exec_timeout_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={0}
          disabled={!form.execEnabled}
          value={form.cronExecTimeoutMinutes}
          onChange={(e) =>
            onFieldChange("cronExecTimeoutMinutes", e.target.value)
          }
        />
      </Field>
    </ConfigSectionCard>
  )
}
