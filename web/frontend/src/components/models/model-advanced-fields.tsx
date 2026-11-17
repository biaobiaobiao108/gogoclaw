import { useTranslation } from "react-i18next"

import {
  AdvancedSection,
  Field,
  SwitchCardField,
} from "@/components/shared-form"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

export interface ModelAdvancedFieldsValue {
  proxy: string
  authMethod: string
  connectMode: string
  workspace: string
  requestTimeout: string
  inputCostPerMillionUSD: string
  outputCostPerMillionUSD: string
  rpm: string
  thinkingLevel: string
  maxTokensField: string
  toolSchemaTransform: string
  streamingEnabled: boolean
  extraBody: string
  customHeaders: string
}

interface ModelAdvancedFieldsProps {
  value: ModelAdvancedFieldsValue
  onChange: <K extends keyof ModelAdvancedFieldsValue>(
    key: K,
    value: ModelAdvancedFieldsValue[K],
  ) => void
  authMethodLocked: boolean
  defaultAuthMethod: string
}

export function ModelAdvancedFields({
  value,
  onChange,
  authMethodLocked,
  defaultAuthMethod,
}: ModelAdvancedFieldsProps) {
  const { t } = useTranslation()

  return (
    <AdvancedSection>
      <Field label={t("models.field.proxy")} hint={t("models.field.proxyHint")}>
        <Input
          value={value.proxy}
          onChange={(event) => onChange("proxy", event.target.value)}
          placeholder="http://127.0.0.1:7890"
        />
      </Field>

      <Field
        label={t("models.field.authMethod")}
        hint={
          authMethodLocked
            ? t("models.field.authMethodManagedHint")
            : t("models.field.authMethodHint")
        }
      >
        <Input
          value={authMethodLocked ? defaultAuthMethod : value.authMethod}
          onChange={(event) => onChange("authMethod", event.target.value)}
          placeholder="oauth"
          disabled={authMethodLocked}
        />
      </Field>

      <Field
        label={t("models.field.connectMode")}
        hint={t("models.field.connectModeHint")}
      >
        <Input
          value={value.connectMode}
          onChange={(event) => onChange("connectMode", event.target.value)}
          placeholder="stdio"
        />
      </Field>

      <Field
        label={t("models.field.workspace")}
        hint={t("models.field.workspaceHint")}
      >
        <Input
          value={value.workspace}
          onChange={(event) => onChange("workspace", event.target.value)}
          placeholder="/path/to/workspace"
        />
      </Field>

      <Field
        label={t("models.field.requestTimeout")}
        hint={t("models.field.requestTimeoutHint")}
      >
        <Input
          value={value.requestTimeout}
          onChange={(event) => onChange("requestTimeout", event.target.value)}
          placeholder="60"
          type="number"
          min={0}
        />
      </Field>

      <Field
        label={t("models.field.inputCost")}
        hint={t("models.field.inputCostHint")}
      >
        <Input
          value={value.inputCostPerMillionUSD}
          onChange={(event) =>
            onChange("inputCostPerMillionUSD", event.target.value)
          }
          placeholder="0"
          type="number"
          min={0}
          step="0.000001"
        />
      </Field>

      <Field
        label={t("models.field.outputCost")}
        hint={t("models.field.outputCostHint")}
      >
        <Input
          value={value.outputCostPerMillionUSD}
          onChange={(event) =>
            onChange("outputCostPerMillionUSD", event.target.value)
          }
          placeholder="0"
          type="number"
          min={0}
          step="0.000001"
        />
      </Field>

      <Field label={t("models.field.rpm")} hint={t("models.field.rpmHint")}>
        <Input
          value={value.rpm}
          onChange={(event) => onChange("rpm", event.target.value)}
          placeholder="60"
          type="number"
          min={0}
        />
      </Field>

      <Field
        label={t("models.field.thinkingLevel")}
        hint={t("models.field.thinkingLevelHint")}
      >
        <Input
          value={value.thinkingLevel}
          onChange={(event) => onChange("thinkingLevel", event.target.value)}
          placeholder={t("models.field.providerDefault")}
        />
      </Field>

      <Field
        label={t("models.field.maxTokensField")}
        hint={t("models.field.maxTokensFieldHint")}
      >
        <Input
          value={value.maxTokensField}
          onChange={(event) => onChange("maxTokensField", event.target.value)}
          placeholder="max_completion_tokens"
        />
      </Field>

      <Field
        label={t("models.field.toolSchemaTransform")}
        hint={t("models.field.toolSchemaTransformHint")}
      >
        <Input
          value={value.toolSchemaTransform}
          onChange={(event) =>
            onChange("toolSchemaTransform", event.target.value)
          }
          placeholder="google"
        />
      </Field>

      <SwitchCardField
        label={t("models.field.streamingEnabled")}
        hint={t("models.field.streamingEnabledHint")}
        checked={value.streamingEnabled}
        onCheckedChange={(checked) => onChange("streamingEnabled", checked)}
        ariaLabel={t("models.field.streamingEnabled")}
      />

      <Field
        label={t("models.field.extraBody")}
        hint={t("models.field.extraBodyHint")}
      >
        <Textarea
          value={value.extraBody}
          onChange={(event) => onChange("extraBody", event.target.value)}
          placeholder='{"key": "value"}'
          rows={3}
        />
      </Field>

      <Field
        label={t("models.field.customHeaders")}
        hint={t("models.field.customHeadersHint")}
      >
        <Textarea
          value={value.customHeaders}
          onChange={(event) => onChange("customHeaders", event.target.value)}
          placeholder='{"X-Source": "coding-plan"}'
          rows={3}
        />
      </Field>
    </AdvancedSection>
  )
}
