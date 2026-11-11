import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type {
  CoreConfigForm,
  MCPServerForm,
  MCPServerType,
} from "@/components/config/form-model"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
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

interface MCPSectionProps {
  form: CoreConfigForm
  onFieldChange: UpdateCoreField
  onAddServer: () => void
  onRemoveServer: (id: string) => void
  onServerFieldChange: <K extends keyof MCPServerForm>(
    id: string,
    key: K,
    value: MCPServerForm[K],
  ) => void
}

export function MCPSection({
  form,
  onFieldChange,
  onAddServer,
  onRemoveServer,
  onServerFieldChange,
}: MCPSectionProps) {
  const { t } = useTranslation()

  return (
    <ConfigSectionCard
      title={t("pages.config.sections.mcp")}
      description={t("pages.config.mcp_section_hint")}
    >
      <SwitchCardField
        label={t("pages.config.mcp_enabled")}
        hint={t("pages.config.mcp_enabled_hint")}
        layout="setting-row"
        checked={form.mcpEnabled}
        onCheckedChange={(checked) => onFieldChange("mcpEnabled", checked)}
      />

      {form.mcpEnabled && (
        <>
          <Field
            label={t("pages.config.mcp_servers")}
            hint={t("pages.config.mcp_servers_hint")}
            layout="setting-row"
            controlClassName="md:max-w-2xl"
          >
            <div className="flex flex-col gap-3">
              {form.mcpServers.map((server) => (
                <div key={server.id} className="bg-background rounded-lg p-4">
                  <div className="mb-3 flex items-center justify-between">
                    <div className="text-sm font-medium">
                      {server.name.trim() || t("pages.config.mcp_server_new")}
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => onRemoveServer(server.id)}
                    >
                      <IconTrash className="size-4" />
                      {t("pages.config.mcp_server_remove")}
                    </Button>
                  </div>

                  <div className="grid gap-3 md:grid-cols-2">
                    <Input
                      value={server.name}
                      placeholder={t(
                        "pages.config.mcp_server_name_placeholder",
                      )}
                      aria-label={t("pages.config.mcp_server_name_placeholder")}
                      onChange={(e) =>
                        onServerFieldChange(server.id, "name", e.target.value)
                      }
                    />

                    <Select
                      value={server.type}
                      onValueChange={(value) =>
                        onServerFieldChange(
                          server.id,
                          "type",
                          value as MCPServerType,
                        )
                      }
                    >
                      <SelectTrigger
                        aria-label={t("pages.config.mcp_server_discovery_mode")}
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="stdio">stdio</SelectItem>
                        <SelectItem value="sse">sse</SelectItem>
                        <SelectItem value="http">http</SelectItem>
                      </SelectContent>
                    </Select>

                    <SwitchCardField
                      label={t("pages.config.mcp_server_enabled")}
                      layout="setting-row"
                      checked={server.enabled}
                      onCheckedChange={(checked) =>
                        onServerFieldChange(server.id, "enabled", checked)
                      }
                    />
                  </div>

                  {server.type !== "stdio" ? (
                    <div className="mt-3 grid gap-3">
                      <Input
                        value={server.url}
                        placeholder={t(
                          "pages.config.mcp_server_url_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_url_placeholder",
                        )}
                        onChange={(e) =>
                          onServerFieldChange(server.id, "url", e.target.value)
                        }
                      />
                      <Textarea
                        value={server.headersText}
                        placeholder={t(
                          "pages.config.mcp_server_headers_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_headers_placeholder",
                        )}
                        className="min-h-[88px] font-mono text-xs"
                        onChange={(e) =>
                          onServerFieldChange(
                            server.id,
                            "headersText",
                            e.target.value,
                          )
                        }
                      />
                    </div>
                  ) : (
                    <div className="mt-3 grid gap-3">
                      <Input
                        value={server.command}
                        placeholder={t(
                          "pages.config.mcp_server_command_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_command_placeholder",
                        )}
                        onChange={(e) =>
                          onServerFieldChange(
                            server.id,
                            "command",
                            e.target.value,
                          )
                        }
                      />
                      <Input
                        value={server.envFile}
                        placeholder={t(
                          "pages.config.mcp_server_env_file_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_env_file_placeholder",
                        )}
                        onChange={(e) =>
                          onServerFieldChange(
                            server.id,
                            "envFile",
                            e.target.value,
                          )
                        }
                      />
                      <Textarea
                        value={server.argsText}
                        placeholder={t(
                          "pages.config.mcp_server_args_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_args_placeholder",
                        )}
                        className="min-h-[88px] font-mono text-xs"
                        onChange={(e) =>
                          onServerFieldChange(
                            server.id,
                            "argsText",
                            e.target.value,
                          )
                        }
                      />
                      <Textarea
                        value={server.envText}
                        placeholder={t(
                          "pages.config.mcp_server_env_placeholder",
                        )}
                        aria-label={t(
                          "pages.config.mcp_server_env_placeholder",
                        )}
                        className="min-h-[88px] font-mono text-xs"
                        onChange={(e) =>
                          onServerFieldChange(
                            server.id,
                            "envText",
                            e.target.value,
                          )
                        }
                      />
                    </div>
                  )}
                </div>
              ))}

              <div>
                <Button type="button" variant="outline" onClick={onAddServer}>
                  <IconPlus className="size-4" />
                  {t("pages.config.mcp_server_add")}
                </Button>
              </div>
            </div>
          </Field>
        </>
      )}
    </ConfigSectionCard>
  )
}
