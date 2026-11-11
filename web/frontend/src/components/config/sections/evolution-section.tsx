import { useTranslation } from "react-i18next"

import type { CoreConfigForm } from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

import { ConfigSectionCard, type UpdateCoreField } from "./config-section-card"

interface EvolutionSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
}

export function EvolutionSection({
  form,
  onFieldChange,
}: EvolutionSectionProps) {
  const { t } = useTranslation()

  return (
    <ConfigSectionCard
      title={t("pages.config.sections.evolution")}
      description={t("pages.config.evolution_section_hint")}
    >
      <SwitchCardField
        label={t("pages.config.evolution_enabled")}
        hint={t("pages.config.evolution_enabled_hint")}
        layout="setting-row"
        checked={form.evolutionEnabled}
        onCheckedChange={(checked) =>
          onFieldChange("evolutionEnabled", checked)
        }
      />

      <Field
        label={t("pages.config.evolution_mode")}
        hint={t("pages.config.evolution_mode_hint")}
        layout="setting-row"
      >
        <Select
          value={form.evolutionMode}
          onValueChange={(value) => onFieldChange("evolutionMode", value)}
        >
          <SelectTrigger aria-label={t("pages.config.evolution_mode")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="observe">
              {t("pages.config.evolution_mode_observe")}
            </SelectItem>
            <SelectItem value="draft">
              {t("pages.config.evolution_mode_draft")}
            </SelectItem>
            <SelectItem value="apply">
              {t("pages.config.evolution_mode_apply")}
            </SelectItem>
          </SelectContent>
        </Select>
      </Field>

      <Field
        label={t("pages.config.evolution_state_dir")}
        hint={t("pages.config.evolution_state_dir_hint")}
        layout="setting-row"
      >
        <Input
          value={form.evolutionStateDir}
          onChange={(e) => onFieldChange("evolutionStateDir", e.target.value)}
          placeholder="e.g. /var/lib/gogoclaw/evolution"
        />
      </Field>

      <Field
        label={t("pages.config.evolution_min_task_count")}
        hint={t("pages.config.evolution_min_task_count_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          value={form.evolutionMinTaskCount}
          onChange={(e) =>
            onFieldChange("evolutionMinTaskCount", e.target.value)
          }
        />
      </Field>

      <Field
        label={t("pages.config.evolution_min_success_ratio")}
        hint={t("pages.config.evolution_min_success_ratio_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={0.01}
          max={1}
          step="0.05"
          value={form.evolutionMinSuccessRatio}
          onChange={(e) =>
            onFieldChange("evolutionMinSuccessRatio", e.target.value)
          }
        />
      </Field>

      <Field
        label={t("pages.config.evolution_cold_path_trigger")}
        hint={t("pages.config.evolution_cold_path_trigger_hint")}
        layout="setting-row"
      >
        <Select
          value={form.evolutionColdPathTrigger}
          onValueChange={(value) =>
            onFieldChange("evolutionColdPathTrigger", value)
          }
        >
          <SelectTrigger
            aria-label={t("pages.config.evolution_cold_path_trigger")}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="after_turn">
              {t("pages.config.evolution_cold_path_after_turn")}
            </SelectItem>
            <SelectItem value="scheduled">
              {t("pages.config.evolution_cold_path_scheduled")}
            </SelectItem>
            <SelectItem value="manual">
              {t("pages.config.evolution_cold_path_manual")}
            </SelectItem>
          </SelectContent>
        </Select>
      </Field>

      {form.evolutionColdPathTrigger === "scheduled" && (
        <Field
          label={t("pages.config.evolution_cold_path_times")}
          hint={t("pages.config.evolution_cold_path_times_hint")}
          layout="setting-row"
        >
          <Textarea
            value={form.evolutionColdPathTimesText}
            placeholder={"03:00\n15:30"}
            className="min-h-[88px] font-mono text-xs"
            onChange={(e) =>
              onFieldChange("evolutionColdPathTimesText", e.target.value)
            }
          />
        </Field>
      )}
    </ConfigSectionCard>
  )
}
