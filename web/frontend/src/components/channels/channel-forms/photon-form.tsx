import { useTranslation } from "react-i18next"

import type { ChannelConfig } from "@/api/channels"
import {
  type ArrayFieldFlusher,
  ChannelArrayListField,
} from "@/components/channels/channel-array-list-field"
import {
  asStringArray,
  parseAllowFromInput,
} from "@/components/channels/channel-array-utils"
import { getSecretInputPlaceholder } from "@/components/channels/channel-config-fields"
import { Field, KeyInput, SwitchCardField } from "@/components/shared-form"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"

import { GenericForm } from "./generic-form"

interface PhotonFormProps {
  config: ChannelConfig
  onChange: (key: string, value: unknown) => void
  configuredSecrets: string[]
  fieldErrors?: Record<string, string>
  registerArrayFieldFlusher?: (
    fieldPath: string,
    flusher: ArrayFieldFlusher | null,
  ) => void
  arrayFieldResetVersion?: number
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function asBool(value: unknown): boolean {
  return value === true
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

const PHOTON_CUSTOM_FIELDS = [
  "project_id",
  "project_secret",
  "disable_read_receipts",
  "cloud_url",
  "allow_from",
  "group_trigger",
]

export function PhotonForm({
  config,
  onChange,
  configuredSecrets,
  fieldErrors = {},
  registerArrayFieldFlusher,
  arrayFieldResetVersion,
}: PhotonFormProps) {
  const { t } = useTranslation()
  const groupTriggerConfig = asRecord(config.group_trigger)

  return (
    <div className="space-y-6">
      <Card className="shadow-sm">
        <CardContent className="space-y-6 px-4 py-0 sm:px-6">
          <Field
            label={t("channels.field.projectId")}
            required
            hint={t("channels.form.desc.projectId")}
            error={fieldErrors.project_id}
          >
            <Input
              value={asString(config.project_id)}
              onChange={(e) => onChange("project_id", e.target.value)}
              autoComplete="off"
            />
          </Field>

          <Field
            label={t("channels.field.projectSecret")}
            required
            hint={t("channels.form.desc.projectSecret")}
            error={fieldErrors.project_secret}
          >
            <KeyInput
              value={asString(config._project_secret)}
              onChange={(v) => onChange("_project_secret", v)}
              placeholder={getSecretInputPlaceholder(
                configuredSecrets,
                "project_secret",
                t("channels.field.secretHintSet"),
                "",
              )}
            />
          </Field>

          <SwitchCardField
            label={t("channels.field.disableReadReceipts")}
            hint={t("channels.form.desc.disableReadReceipts")}
            checked={asBool(config.disable_read_receipts)}
            onCheckedChange={(checked) =>
              onChange("disable_read_receipts", checked)
            }
            ariaLabel={t("channels.field.disableReadReceipts")}
          />
        </CardContent>
      </Card>

      <Card className="shadow-sm">
        <CardContent className="space-y-6 px-4 py-0 sm:px-6">
          <ChannelArrayListField
            label={t("channels.field.allowFrom")}
            hint={t("channels.form.desc.allowFrom")}
            value={asStringArray(config.allow_from)}
            onChange={(value) => onChange("allow_from", value)}
            placeholder="+15551234567"
            parser={parseAllowFromInput}
            fieldPath="allow_from"
            registerFlusher={registerArrayFieldFlusher}
            resetVersion={arrayFieldResetVersion}
          />

          <SwitchCardField
            label={t("channels.field.groupTriggerMentionOnly")}
            hint={t("channels.form.desc.groupTriggerMentionOnly")}
            checked={asBool(groupTriggerConfig.mention_only)}
            onCheckedChange={(checked) =>
              onChange("group_trigger", {
                ...groupTriggerConfig,
                mention_only: checked,
              })
            }
            ariaLabel={t("channels.field.groupTriggerMentionOnly")}
          />

          <ChannelArrayListField
            label={t("channels.field.groupTriggerPrefixes")}
            hint={t("channels.form.desc.groupTriggerPrefixes")}
            value={asStringArray(groupTriggerConfig.prefixes)}
            onChange={(value) =>
              onChange("group_trigger", {
                ...groupTriggerConfig,
                prefixes: value,
              })
            }
            placeholder={t("channels.field.groupTriggerPrefixes")}
            fieldPath="group_trigger.prefixes"
            registerFlusher={registerArrayFieldFlusher}
            resetVersion={arrayFieldResetVersion}
          />
        </CardContent>
      </Card>

      <GenericForm
        config={config}
        onChange={onChange}
        configuredSecrets={configuredSecrets}
        hiddenKeys={PHOTON_CUSTOM_FIELDS}
        fieldErrors={fieldErrors}
        registerArrayFieldFlusher={registerArrayFieldFlusher}
        arrayFieldResetVersion={arrayFieldResetVersion}
        supportsStreaming
      />
    </div>
  )
}
