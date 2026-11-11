import { useTranslation } from "react-i18next"

import {
  type CoreConfigForm,
  DM_SCOPE_OPTIONS,
} from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

import { ConfigSectionCard, type UpdateCoreField } from "./config-section-card"

interface RuntimeSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
}

export function RuntimeSection({ form, onFieldChange }: RuntimeSectionProps) {
  const { t } = useTranslation()
  const selectedDmScopeOption = DM_SCOPE_OPTIONS.find(
    (scope) => scope.value === form.dmScope,
  )

  return (
    <ConfigSectionCard title={t("pages.config.sections.runtime")}>
      <Field
        label={t("pages.config.session_scope")}
        hint={t("pages.config.session_scope_hint")}
        layout="setting-row"
      >
        <Select
          value={form.dmScope}
          onValueChange={(value) => onFieldChange("dmScope", value)}
        >
          <SelectTrigger className="w-full">
            <SelectValue>
              {selectedDmScopeOption
                ? t(
                    selectedDmScopeOption.labelKey,
                    selectedDmScopeOption.labelDefault,
                  )
                : form.dmScope}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {DM_SCOPE_OPTIONS.map((scope) => (
              <SelectItem key={scope.value} value={scope.value}>
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium">{t(scope.labelKey)}</span>
                  <span className="text-muted-foreground text-xs">
                    {t(scope.descKey)}
                  </span>
                </div>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      <SwitchCardField
        label={t("pages.config.heartbeat_enabled")}
        hint={t("pages.config.heartbeat_enabled_hint")}
        layout="setting-row"
        checked={form.heartbeatEnabled}
        onCheckedChange={(checked) =>
          onFieldChange("heartbeatEnabled", checked)
        }
      />

      {form.heartbeatEnabled && (
        <Field
          label={t("pages.config.heartbeat_interval")}
          hint={t("pages.config.heartbeat_interval_hint")}
          layout="setting-row"
        >
          <Input
            type="number"
            min={1}
            value={form.heartbeatInterval}
            onChange={(e) => onFieldChange("heartbeatInterval", e.target.value)}
          />
        </Field>
      )}
    </ConfigSectionCard>
  )
}
