---
kind: error_handling
name: 错误处理：轻量级日志 + 直接返回 error 的 Go 风格实践
category: error_handling
scope:
    - '**'
source_files:
    - main.go
---

本仓库为单文件 Go 程序（main.go），未引入任何第三方错误库，也未定义专用错误类型或错误码包。整体采用 Go 标准库约定的函数返回 error 模式，结合 log 包打印上下文信息，属于最轻量的错误处理方式。

1. 使用方式与模式
- 业务函数通过返回值传递错误：如 listenSOCKS5() error、socks5Handshake(conn) error、socks5ParseRequest(conn) (string, error) 等；调用方在 err != nil 时记录日志并 return/continue。
- 语义性错误使用 errors.New("...") 构造简单错误值，例如 unsupported socks version、unsupported command、IPv4 not supported、unsupported address type，便于上层按字符串匹配或仅用于日志输出。
- HTTP 层使用 http.Error(w, msg, code) 向客户端返回状态码（405、503、500、502），不包装 error。
- 启动阶段配置校验失败使用 log.Fatalf(...) 直接退出进程，避免携带无效配置继续运行。

2. 关键位置
- main 入口：解析参数、CIDR 校验、监听端口失败均 log.Fatalf 后终止。
- handleHTTP：连接数超限返回 503；Hijack 不支持返回 500；出站拨号失败写 502 响应。
- listenSOCKS5/handleSOCKS5：accept 失败仅 log 并 continue；握手/请求解析失败 log 后返回；拨号失败发送 SOCKS5 拒绝码（0x04）。
- socks5Handshake / socks5ParseRequest：对协议版本、命令、地址族、地址类型的非法输入返回 errors.New 错误。

3. 架构约定与缺失点
- 无 panic/recover 机制，所有异常路径均以 error 返回值或 http.Error 表达。
- 未定义统一错误类型或错误码常量，错误信息以硬编码字符串形式散落在各方法中。
- 未实现中间件式全局错误捕获；每个 goroutine 的错误都在各自函数内就地处理。
- 并发控制通过 channel 信号量实现，超限直接拒绝而非排队重试。

4. 开发者应遵循的规则
- 新增功能优先返回 error，由调用方决定是记录日志、返回 HTTP 状态码还是向上层传播。
- 需要区分不同错误场景时，继续使用 errors.New("...") 保持简洁；若需跨模块识别，可考虑集中定义错误变量或自定义错误类型。
- 对外暴露的 HTTP/SOCKS5 响应应保持明确的状态码或协议字段，不要静默吞掉错误。
- 启动期不可恢复的错误继续使用 log.Fatalf，运行时可恢复错误则返回 error。