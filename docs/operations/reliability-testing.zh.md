# 可靠性检查

可通过环境变量扩展确定性的多会话重启测试，不改变 CI 的默认轮数：

```sh
GOGOCLAW_HARNESS_RESTART_ROUNDS=50 go test -tags goolm,stdjson ./pkg/harness -run '^TestProductFlowConcurrentSessionsSurviveRestarts$' -count=1 -v
```

每轮针对 SQLite 并发运行 8 个会话，然后关闭并重新打开存储。最终检查完整会话记录，包括已从模型上下文压缩掉的消息。进度日志包含运行时间、goroutine 数量和 Go 堆分配。长时间测试应相应调整轮数和 Go 测试超时。这是进程内的确定性测试，不测量容器 RSS，也不能证明真实模型和渠道的恢复能力。

Reliability stress 工作流每周执行 50 轮，也可手动运行，日志作为制品保留 30 天。Benchmark 工作流也每周运行并保存环境和结果；判断性能回退时应比较运行环境和配置相同的报告。

独立 gateway 的 Compose 健康检查使用 `/ready`，表示 gateway 已完成启动；`/health` 仅表示 HTTP 进程有响应。Launcher 的 Compose 检查使用独立的 `/health`，即使尚未设置首个模型也能反映控制台存活。已登录的监控端可以请求 Launcher 的 `/api/gateway/readiness` 检查网关就绪状态。这些检查均不能证明模型或外部频道可用，需查看控制台状态并发送测试消息确认。

`TestStoppedDataBackupRestoresSessionAndWorkspace` 验证停止写入后复制数据并在新目录恢复，包括会话、附件、工作区文件、定时任务、模型配置以及使用测试密钥加密的凭据。部署备份仍应遵循 [Docker 部署指南](../guides/docker.zh.md) 的停机备份步骤；该测试不能代替实际容器恢复演练，也不覆盖存放在数据目录之外的密钥。

CI 浏览器任务还会使用临时数据目录启动真实 launcher，检查首次设置、错误密码、成功登录、模型持久化和审批设置。测试通过真实 API 创建模型，并在刷新后从界面验证；审批开关通过界面修改，检查自定义规则是否保留。浏览器测试还通过仅监听本机的模拟模型启动网关，发送一条消息，刷新核对历史，并检查 Launcher 日志输出中是否泄露输入或回复。

应用的统一日志组件会遮蔽常见凭据格式和敏感结构化字段名，但不能保证识别所有凭据、标准库及其他程序的日志或没有可识别标签的数据。应限制日志访问，并在分享诊断信息前检查内容。

## 文本投递与日志

Telegram、微信、Discord、飞书、QQ 和 Pico Client 会记录中途文本回复、最终文本回复、定时输出以及 `message` 工具文本的近期投递状态。控制台「会话管理」可以查询和刷新该状态。平台返回回执后才标记为「平台已接收」，Pico Client 等待对端的 `message.ack`。发送中断、超时或长消息只发出部分时，状态记为未知。网关重启后会重新调度尚未开始发送的排队消息。渠道能确认可安全重试时，发送任务会进行有限次重试。

Web 聊天在同一个气泡内更新流式草稿，并在文本完整后确认接收，工具调用前的中途回复也会收到确认。对端提供 Pico 流式和投递标记时，Pico Client 会转发完成的流式内容。

常规对话日志记录回合 ID、模型、工具名和消息长度等诊断信息。需要采集请求或回复预览时，使用 DEBUG 并在启动 Gateway 前设置 `GOGOCLAW_CONTENT_DIAGNOSTICS_UNTIL` 为将来的 RFC 3339 时间。到期后新日志自动停止写入正文预览，已有日志仍需按原有保留策略处理。

## 容器升级与会话分页

在隔离的 Docker 环境运行容器恢复演练。可选的基准镜像用于在独立的数据卷上验证升级和回滚。

```sh
GOGOCLAW_TEST_BASELINE_IMAGE=gogoclaw:previous GOGOCLAW_TEST_IMAGE=gogoclaw:candidate bash scripts/test-container-restart.sh
```

在本地可用以下命令比较同一批 300 条会话下，新 SQLite 分页与兼容路径的首屏 P95。冷测每次创建新 Handler，操作系统仍可能缓存文件。

```sh
go test -tags goolm,stdjson ./web/backend/api -run '^$' -bench '^BenchmarkSQLiteSessionListPage$' -benchtime=25x -count=1
```
