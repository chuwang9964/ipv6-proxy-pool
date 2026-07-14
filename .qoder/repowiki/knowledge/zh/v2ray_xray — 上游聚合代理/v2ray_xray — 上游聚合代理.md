---
kind: external_dependency
name: v2ray/xray — 上游聚合代理
slug: v2ray-xray
category: external_dependency
category_hints:
    - client_constraint
scope:
    - '**'
---

### v2ray/xray
- 角色：项目文档与证书脚本将其定位为“上游”——ipv6-proxy-pool 作为其出站代理，配合 smux 多路复用缓解路由器 conntrack 表压力。
- 集成点：`scripts/gen_cert.sh` 默认将自签证书输出到 `/usr/local/etc/xray`，说明该仓库面向 xray 生态；README 明确建议与 v2ray/xray 搭配使用。
- 约束：仅作为上游消费 ipv6-proxy-pool 的 HTTP CONNECT/SOCKS5 出口，不直接依赖其 API。