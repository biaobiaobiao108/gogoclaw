# GoGoClaw 文档

GoGoClaw 是一个单机私有化部署的 agent。一个可信实例独占一个数据目录；多租户、RBAC 和横向扩容不在范围内。文档按文档类型组织。英文文档使用基础文件名，中文翻译使用 `.zh.md` 后缀并放在源文件旁边。仓库根目录默认提供中文 README，并在 `README.en.md` 提供英文 README。

## 读者导航

- [指南](guides/README.md)：安装、配置、Provider 和工作流指南。
- [参考](reference/README.md)：精确的配置与行为参考。
- [运维](operations/README.md)：调试、故障排查和模型行为评测。
- [安全](security/README.md)：安全专题指南与控制措施。
- [架构](architecture/README.md)：实现说明和内部设计文档。
- [迁移](migration/README.md)：升级与迁移说明。

渠道专用配置请先阅读 [聊天应用配置](guides/chat-apps.md)，然后按需使用 `docs/channels/<name>/README.md`。CI fixture 与真实平台检查的区别参见 [渠道验证范围](channels/validation.md)。

## 翻译规则

- 英文文档使用基础文件名，例如 `README.md` 或 `configuration.md`。
- 中文翻译与英文源文件放在同一目录，并使用 `.zh.md`，例如 `configuration.zh.md`。
- 不维护其他语言区域后缀。
- 项目入口文档位于仓库根目录；与代码相邻的翻译放在其所描述的代码旁边。

## 目录结构

- `docs/guides/`：安装与使用指南。
- `docs/reference/`：详细的配置与行为参考。
- `docs/operations/`：调试与故障排查。
- `docs/security/`：安全文档。
- `docs/architecture/`：内部设计说明。
- `docs/channels/`：渠道专用集成指南。
- `docs/design/`：设计方案与调查记录。
- `docs/migration/`：迁移说明。
