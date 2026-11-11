import { useTranslation } from "react-i18next"

import type {
  CoreConfigForm,
  TurnProfileForm,
  TurnProfileMode,
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
import { Textarea } from "@/components/ui/textarea"

import { ConfigSectionCard, type UpdateCoreField } from "./config-section-card"

interface AgentDefaultsSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
  onTurnProfileFieldChange: <K extends keyof TurnProfileForm>(
    key: K,
    value: TurnProfileForm[K],
  ) => void
}

export function AgentDefaultsSection({
  form,
  onFieldChange,
  onTurnProfileFieldChange,
}: AgentDefaultsSectionProps) {
  const { t } = useTranslation()
  const renderModeSelect = ({
    value,
    onValueChange,
    allowCustom,
  }: {
    value: TurnProfileMode
    onValueChange: (mode: TurnProfileMode) => void
    allowCustom: boolean
  }) => (
    <Select
      value={value}
      onValueChange={(next) => onValueChange(next as TurnProfileMode)}
    >
      <SelectTrigger className="h-9">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="default">
          {t("pages.config.turn_profile_mode_default")}
        </SelectItem>
        <SelectItem value="off">
          {t("pages.config.turn_profile_mode_off")}
        </SelectItem>
        {allowCustom && (
          <SelectItem value="custom">
            {t("pages.config.turn_profile_mode_custom")}
          </SelectItem>
        )}
      </SelectContent>
    </Select>
  )

  return (
    <ConfigSectionCard title={t("pages.config.sections.agent")}>
      <Field
        label={t("pages.config.workspace")}
        hint={t("pages.config.workspace_hint")}
        layout="setting-row"
      >
        <Input
          value={form.workspace}
          onChange={(e) => onFieldChange("workspace", e.target.value)}
          placeholder="~/.gogoclaw/workspace"
        />
      </Field>

      <SwitchCardField
        label={t("pages.config.restrict_workspace")}
        hint={t("pages.config.restrict_workspace_hint")}
        layout="setting-row"
        checked={form.restrictToWorkspace}
        onCheckedChange={(checked) =>
          onFieldChange("restrictToWorkspace", checked)
        }
      />

      <SwitchCardField
        label={t("pages.config.split_on_marker")}
        hint={t("pages.config.split_on_marker_hint")}
        layout="setting-row"
        checked={form.splitOnMarker}
        onCheckedChange={(checked) => onFieldChange("splitOnMarker", checked)}
      />

      <SwitchCardField
        label={t("pages.config.tool_feedback_enabled")}
        hint={t("pages.config.tool_feedback_enabled_hint")}
        layout="setting-row"
        checked={form.toolFeedbackEnabled}
        onCheckedChange={(checked) =>
          onFieldChange("toolFeedbackEnabled", checked)
        }
      />

      {form.toolFeedbackEnabled && (
        <SwitchCardField
          label={t("pages.config.tool_feedback_separate_messages")}
          hint={t("pages.config.tool_feedback_separate_messages_hint")}
          layout="setting-row"
          checked={form.toolFeedbackSeparateMessages}
          onCheckedChange={(checked) =>
            onFieldChange("toolFeedbackSeparateMessages", checked)
          }
        />
      )}

      {form.toolFeedbackEnabled && (
        <Field
          label={t("pages.config.tool_feedback_max_args_length")}
          hint={t("pages.config.tool_feedback_max_args_length_hint")}
          layout="setting-row"
        >
          <Input
            type="number"
            min={0}
            value={form.toolFeedbackMaxArgsLength}
            onChange={(e) =>
              onFieldChange("toolFeedbackMaxArgsLength", e.target.value)
            }
          />
        </Field>
      )}

      <Field
        label={t("pages.config.max_tokens")}
        hint={t("pages.config.max_tokens_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          value={form.maxTokens}
          onChange={(e) => onFieldChange("maxTokens", e.target.value)}
        />
      </Field>

      <Field
        label={t("pages.config.context_window")}
        hint={t("pages.config.context_window_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          value={form.contextWindow}
          onChange={(e) => onFieldChange("contextWindow", e.target.value)}
          placeholder="131072"
        />
      </Field>

      <Field
        label={t("pages.config.max_tool_iterations")}
        hint={t("pages.config.max_tool_iterations_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          value={form.maxToolIterations}
          onChange={(e) => onFieldChange("maxToolIterations", e.target.value)}
        />
      </Field>

      <Field
        label={t("pages.config.summarize_threshold")}
        hint={t("pages.config.summarize_threshold_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          value={form.summarizeMessageThreshold}
          onChange={(e) =>
            onFieldChange("summarizeMessageThreshold", e.target.value)
          }
        />
      </Field>

      <Field
        label={t("pages.config.summarize_token_percent")}
        hint={t("pages.config.summarize_token_percent_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={1}
          max={100}
          value={form.summarizeTokenPercent}
          onChange={(e) =>
            onFieldChange("summarizeTokenPercent", e.target.value)
          }
        />
      </Field>

      <Field
        label={t("pages.config.session_token_budget")}
        hint={t("pages.config.session_token_budget_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={0}
          value={form.sessionTokenBudget}
          onChange={(e) => onFieldChange("sessionTokenBudget", e.target.value)}
        />
      </Field>

      <Field
        label={t("pages.config.daily_token_budget")}
        hint={t("pages.config.daily_token_budget_hint")}
        layout="setting-row"
      >
        <Input
          type="number"
          min={0}
          value={form.dailyTokenBudget}
          onChange={(e) => onFieldChange("dailyTokenBudget", e.target.value)}
        />
      </Field>

      <Field
        label={t("pages.config.budget_exceeded_mode")}
        hint={t("pages.config.budget_exceeded_mode_hint")}
        layout="setting-row"
      >
        <Select
          value={form.budgetExceededMode}
          onValueChange={(value) =>
            onFieldChange("budgetExceededMode", value as "stop" | "warn")
          }
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="stop">
              {t("pages.config.budget_exceeded_stop")}
            </SelectItem>
            <SelectItem value="warn">
              {t("pages.config.budget_exceeded_warn")}
            </SelectItem>
          </SelectContent>
        </Select>
      </Field>

      <Field
        label={t("pages.config.turn_profile")}
        hint={t("pages.config.turn_profile_hint")}
        layout="setting-row"
        controlClassName="md:max-w-[42rem]"
      >
        <div className="space-y-3">
          <SwitchCardField
            label={t("pages.config.turn_profile_enabled")}
            hint={t("pages.config.turn_profile_enabled_hint")}
            checked={form.turnProfile.enabled}
            onCheckedChange={(checked) =>
              onTurnProfileFieldChange("enabled", checked)
            }
          />

          <div className="grid gap-3 lg:grid-cols-2">
            <div className="space-y-2">
              <label className="text-sm font-medium">
                {t("pages.config.turn_profile_history")}
              </label>
              {renderModeSelect({
                value: form.turnProfile.historyMode,
                onValueChange: (mode) =>
                  onTurnProfileFieldChange(
                    "historyMode",
                    mode === "off" ? "off" : "default",
                  ),
                allowCustom: false,
              })}
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t("pages.config.turn_profile_history_hint")}
              </p>
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">
                {t("pages.config.turn_profile_system_prompt")}
              </label>
              {renderModeSelect({
                value: form.turnProfile.systemPromptMode,
                onValueChange: (mode) =>
                  onTurnProfileFieldChange(
                    "systemPromptMode",
                    mode === "off" ? "off" : "default",
                  ),
                allowCustom: false,
              })}
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t("pages.config.turn_profile_system_prompt_hint")}
              </p>
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">
                {t("pages.config.turn_profile_skills")}
              </label>
              {renderModeSelect({
                value: form.turnProfile.skillsMode,
                onValueChange: (mode) =>
                  onTurnProfileFieldChange("skillsMode", mode),
                allowCustom: true,
              })}
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t("pages.config.turn_profile_skills_hint")}
              </p>
              {form.turnProfile.skillsMode === "custom" && (
                <Textarea
                  value={form.turnProfile.skillsAllowText}
                  onChange={(e) =>
                    onTurnProfileFieldChange("skillsAllowText", e.target.value)
                  }
                  placeholder={t(
                    "pages.config.turn_profile_skills_allow_placeholder",
                  )}
                  className="min-h-20 font-mono text-xs"
                />
              )}
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium">
                {t("pages.config.turn_profile_tools")}
              </label>
              {renderModeSelect({
                value: form.turnProfile.toolsMode,
                onValueChange: (mode) =>
                  onTurnProfileFieldChange("toolsMode", mode),
                allowCustom: true,
              })}
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t("pages.config.turn_profile_tools_hint")}
              </p>
              {form.turnProfile.toolsMode === "custom" && (
                <Textarea
                  value={form.turnProfile.toolsAllowText}
                  onChange={(e) =>
                    onTurnProfileFieldChange("toolsAllowText", e.target.value)
                  }
                  placeholder={t(
                    "pages.config.turn_profile_tools_allow_placeholder",
                  )}
                  className="min-h-20 font-mono text-xs"
                />
              )}
            </div>
          </div>
        </div>
      </Field>
    </ConfigSectionCard>
  )
}
