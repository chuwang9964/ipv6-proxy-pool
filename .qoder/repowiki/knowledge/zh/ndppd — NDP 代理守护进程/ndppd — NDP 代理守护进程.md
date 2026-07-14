---
kind: external_dependency
name: ndppd — NDP 代理守护进程
slug: ndppd
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
---

### ndppd
- 角色：在本机通过 `ip route add local <prefix>/64` 绑定任意前缀内 IPv6 地址后，路由器侧仍需要 NDP（邻居发现）将随机出口 IP 解析到本机 MAC；ndppd 作为 NDP 代理把整个 /64 网段静态代理到指定网口，使远端能到达这些随机源 IP。
- 集成点：`scripts/install.sh` 通过 apt 安装并提示复制 `systemd/ndppd.conf.example` 到 `/etc/ndppd.conf`；README 中给出完整配置示例（包含 `proxy <iface>`、`rule <prefix>::/64 { static }` 等）。
- 稳定用法：以 `route-ttl` + `timeout` + `ttl` 控制缓存，对每个 /64 规则使用 `static` 模式，无需为每个随机 IP 单独宣告。