# 可靠性检查

可通过环境变量扩展确定性的多会话重启测试，不改变 CI 的默认轮数：

```sh
GOGOCLAW_HARNESS_RESTART_ROUNDS=50 go test -tags goolm,stdjson ./pkg/harness -run '^TestProductFlowConcurrentSessionsSurviveRestarts$' -count=1 -v
```

每轮针对 SQLite 并发运行 8 个会话，然后关闭并重新打开存储。最终检查完整会话记录，包括已从模型上下文压缩掉的消息。进度日志包含运行时间、goroutine 数量和 Go 堆分配。长时间测试应相应调整轮数和 Go 测试超时。这是进程内的确定性测试，不测量容器 RSS，也不能证明真实模型和渠道的恢复能力。

Reliability stress 工作流每周执行 50 轮，也可手动运行，日志作为制品保留 30 天。Benchmark 工作流也每周运行并保存环境和结果；判断性能回退时应比较运行环境和配置相同的报告。

独立 gateway 的 Compose 健康检查使用 `/ready`，表示 gateway 已完成启动；`/health` 仅表示 HTTP 进程有响应。Launcher 的检查只验证认证接口。这些检查均不能证明模型或外部频道可用，需查看控制台状态并发送测试消息确认。

`TestStoppedDataBackupRestoresSessionAndWorkspace` 验证停止写入后复制数据并在新目录恢复，包括会话、工作区文件、定时任务、模型配置以及使用测试密钥加密的凭据。部署备份仍应遵循 [Docker 部署指南](../guides/docker.zh.md) 的停机备份步骤；该测试不能代替实际容器恢复演练，也不覆盖存放在数据目录之外的密钥。

CI 浏览器任务还会使用临时数据目录启动真实 launcher，检查首次设置、错误密码、成功登录、模型持久化和审批设置。测试通过真实 API 创建模型，并在刷新后从界面验证；审批开关通过界面修改，检查自定义规则是否保留。测试不启动模型，也不验证聊天消息投递。

应用的统一日志组件会遮蔽常见凭据格式和敏感结构化字段名，但不能保证识别所有凭据、标准库及其他程序的日志或没有可识别标签的数据。应限制日志访问，并在分享诊断信息前检查内容。
