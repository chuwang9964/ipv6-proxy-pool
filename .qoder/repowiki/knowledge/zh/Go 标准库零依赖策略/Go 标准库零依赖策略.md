---
kind: dependency_management
name: Go 标准库零依赖策略
category: dependency_management
scope:
    - '**'
source_files:
    - go.mod
    - main.go
---

本项目采用极简的 Go 模块结构，完全基于 Go 标准库实现，未引入任何第三方依赖。具体表现如下：

- **go.mod**：文件存在但为空（无 require 声明），表明项目未声明任何外部依赖。
- **go.sum**：不存在，进一步确认没有第三方模块被拉取或锁定。
- **源码分析**：`main.go` 仅使用 `net`、`net/http`、`encoding/binary`、`sync`、`time`、`flag`、`fmt`、`log`、`io`、`errors`、`math/rand` 等标准库包，所有代理逻辑（HTTP CONNECT、SOCKS5）均自行实现，未借助任何第三方代理库。
- **构建产物**：单二进制输出，无需 vendor 目录或私有仓库配置。

这种“零依赖”设计使程序具备极高的可移植性和安全性——编译结果不随上游模块更新而波动，也无需处理依赖冲突或供应链风险。对于此类轻量级网络工具而言，该策略是合理且推荐的做法。