---
kind: logging_system
name: 基于标准库 log 的简易日志输出
category: logging_system
scope:
    - '**'
source_files:
    - main.go
---

本仓库未引入任何第三方日志框架，也未实现自定义日志子系统。所有日志输出均直接使用 Go 标准库 `log` 包（`import "log"`），通过 `log.Printf`、`log.Fatalf` 等函数以人类可读的字符串形式打印到 stderr。

主要特点：
- **无结构化字段**：日志消息为拼接后的字符串，如 `[HTTP] %s %s`、`[SOCKS5-FAIL] %s via %s: %v`，没有统一的键值对或 JSON 结构，不利于机器解析与聚合。
- **无日志级别管理**：全部使用同一套 `log.Printf`/`log.Fatalf`，没有区分 INFO/WARN/ERROR/FATAL 的分级输出；错误路径和成功路径混在同一级别。
- **无日志开关与配置**：程序启动参数（`-http`、`-socks`、`-prefix`、`-c`）不包含任何日志相关选项，无法在运行时调整输出行为。
- **无日志轮转与持久化**：日志直接输出到进程标准错误流，依赖外部容器或 systemd 收集，程序自身不做文件落盘或轮转。
- **统一前缀标签**：通过 `[HTTP]`、`[SOCKS5]`、`[HTTP-OK]`、`[HTTP-FAIL]`、`[SOCKS5-OK]`、`[SOCKS5-FAIL]` 等方括号标签区分来源模块，是本项目唯一的“约定”。

由于整个项目仅由单个 `main.go` 构成且功能简单，目前这种轻量方式已足够满足调试与运维需求，但尚未形成可复用的 logging_system。