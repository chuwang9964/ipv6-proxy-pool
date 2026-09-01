# IPv6 Proxy Pool

基于 IPv6 /64 或 /112 前缀的随机出口代理池。纯 Go 实现，零外部依赖。

## 核心特性

- **SOCKS5 + HTTP CONNECT** 双协议支持
- **强制 IPv6 出口**：`tcp6` Dial，IPv4-only 目标直接拒绝
- **/112 子网隔离**：同一 /64 下多台服务器互不干扰
- **并发限流**：内置 semaphore，防止资源耗尽
- **与 v2ray/xray 配合**：通过 smux 聚合缓解路由器 conntrack 压力

## 一、确认网络环境

查看服务器的 IPv6 地址，确认网口和 prefix：

```bash
root@192:~# ip addr
2: enp1s0: <BROADCAST,MULTICAST,ALLMULTI,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether e4:54:e8:99:c6:47 brd ff:ff:ff:ff:ff:ff
    inet 192.168.1.250/24 brd 192.168.1.255 scope global enp1s0
       valid_lft forever preferred_lft forever
    inet6 240e:6b0:50::fd5/128 scope global noprefixroute 
       valid_lft forever preferred_lft forever
    inet6 240e:6b0:50:0:e654:e8ff:fe99:c647/64 scope global mngtmpaddr noprefixroute 
       valid_lft forever preferred_lft forever
    inet6 fe80::e654:e8ff:fe99:c647/64 scope link 
       valid_lft forever preferred_lft forever
```

- **网口**：`enp1s0`
- **本机 IPv6 地址**：`240e:6b0:50:0:e654:e8ff:fe99:c647/64`
- **Prefix 选择**：
  - 后期打算在同一路由下添加多台 IPv6 代理 → 选择 `240e:6b0:50:0::/112`
  - 只需要一台 IPv6 代理 → 使用 `240e:6b0:50::/64`

## 二、编译部署

```bash
# 1. 安装 Go（如果还没有）
wget https://go.dev/dl/go1.22.3.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.3.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 2. 编译
sudo mkdir -p /opt/ipv6-proxy
sudo tee /opt/ipv6-proxy/main.go <<'EOF'
# 把上面的 Go 代码完整粘贴进来
EOF

cd /opt/ipv6-proxy
sudo /usr/local/go/bin/go build -o ipv6-proxy main.go
sudo chmod +x /opt/ipv6-proxy/ipv6-proxy
```

## 三、系统配置

### 1. 内核参数

```bash
sudo tee /etc/sysctl.d/99-ipv6-proxy.conf <<'EOF'
net.ipv6.ip_nonlocal_bind = 1
net.ipv6.conf.all.forwarding = 1
net.ipv6.neigh.default.gc_thresh1 = 1024
net.ipv6.neigh.default.gc_thresh2 = 4096
net.ipv6.neigh.default.gc_thresh3 = 102400
net.ipv4.tcp_tw_reuse = 1
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_fin_timeout = 15
EOF
sudo sysctl -p /etc/sysctl.d/99-ipv6-proxy.conf
```

### 2. 本地路由

关键：让内核认为整个前缀都是本地地址。

```bash
# 单台情况
sudo ip route add local 240e:6b0:50::/64 dev enp1s0

# 多台情况
sudo ip route add local 240e:6b0:50:0:1::/112 dev enp1s0
```

> **多台服务器注意**：如果不规划服务器认领 IP 段，会出现多台服务器共同认领一段 IP，导致某台服务器生成的 IPv6 地址失效、路由器异常。可以规划第二台的 IP 段为 `240e:6b0:50:0:2::/112`，以此类推。

### 3. ndppd

static 模式，无条件代理整个前缀的 NDP。多台则换成 `240e:6b0:50:0:1::/112`。

```bash
sudo apt install -y ndppd
sudo tee /etc/ndppd.conf <<'EOF'
route-ttl 30000

proxy enp1s0 {
    router no
    timeout 500
    ttl 30000

    rule 240e:6b0:50::/64 {
        static
    }
}
EOF
sudo systemctl enable ndppd
sudo systemctl restart ndppd
```

## 四、Systemd 服务（高并发版）

多台情况下更改 `-prefix 240e:6b0:50:0:1::/112`。

```bash
sudo tee /etc/systemd/system/ipv6-proxy.service <<'EOF'
[Unit]
Description=IPv6 Rotating Proxy (HTTP+SOCKS5)
After=network-online.target ndppd.service
Wants=network-online.target ndppd.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/ipv6-proxy

# -c 10000: 并发上限 10000（HTTP 和 SOCKS5 共享）
ExecStart=/opt/ipv6-proxy/ipv6-proxy \
    -http 0.0.0.0:53420 \
    -socks 0.0.0.0:53421 \
    -prefix 240e:6b0:50::/64 \
    -c 10000

Restart=always
RestartSec=5
StartLimitInterval=60s
StartLimitBurst=3

# === 资源限制：并发高的核心 ===
LimitNOFILE=1048576
LimitNPROC=65535
OOMScoreAdjust=-800

StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipv6-proxy

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now ipv6-proxy
sudo journalctl -u ipv6-proxy -f
```

## 五、验证与测试

```bash
# 1. 确认服务启动
sudo systemctl status ipv6-proxy

# 2. 测试 HTTP 代理（轮换 IPv6）
for i in {1..5}; do
  curl -s -x http://127.0.0.1:53420 https://api6.ipify.org
  echo
done

# 3. 测试 SOCKS5 代理
for i in {1..5}; do
  curl -s --socks5 127.0.0.1:53421 https://api6.ipify.org
  echo
done

# 4. 确认 IPv4-only 目标被拒绝
curl -x http://127.0.0.1:53420 --connect-timeout 5 https://v4.ident.me
# 期望：502 Bad Gateway

curl --socks5 127.0.0.1:53421 --connect-timeout 5 https://v4.ident.me
# 期望：连接失败（SOCKS5 返回 0x04 host unreachable）

# 5. 看日志确认源 IP 在 240e:6b0:50::/112 范围内
sudo journalctl -u ipv6-proxy -n 30 --no-pager
```

## 配合 v2ray 使用（推荐）

通过 v2ray 作为代理出口，提供 VMess 和 HTTP 两种入站协议，出站流量经 ipv6-proxy 的 SOCKS5 端口随机 IPv6 出口。VMess 协议支持多路复用，可减少长连接数量，降低路由器 conntrack 压力。

### 架构

```
客户端A → VMess(TLS) → v2ray:443 ─┐
                                    ├→ SOCKS5 → ipv6-proxy → 随机 IPv6 → 目标
客户端B → HTTP 代理  → v2ray:8080 ─┘
```

### 部署步骤

1. 生成 TLS 证书
```bash
sudo bash scripts/gen_cert.sh
```

2. 生成 UUID
```bash
v2ray uuid
```

3. 配置 v2ray（参考 `v2ray/config.json.example`）
```bash
sudo mkdir -p /usr/local/etc/v2ray
sudo cp v2ray/config.json.example /usr/local/etc/v2ray/config.json
# 编辑 config.json，替换 UUID 和密码
sudo nano /usr/local/etc/v2ray/config.json
```

配置说明：
- 入站1（VMess）：监听 443 端口，TLS 加密，客户端通过 VMess 协议连接
- 入站2（HTTP）：监听 8080 端口，HTTP 代理，支持用户名密码认证
- 出站：SOCKS5 指向 `127.0.0.1:53421`（ipv6-proxy 的 SOCKS5 端口）

4. 启动服务
```bash
# 先确保 ipv6-proxy 已启动
sudo systemctl start ipv6-proxy

# 启动 v2ray
sudo systemctl start v2ray
```

### 客户端使用

**VMess 方式**（推荐，支持多路复用）：
- 服务端地址：你的服务器 IP
- 端口：443
- 协议：VMess
- UUID：你生成的 UUID
- TLS：开启

**HTTP 方式**（简单直接）：
```bash
curl --proxy http://admin:your-password@your-server-ip:8080 https://api6.ipify.org
```

### 效果

- **VMess 入站**：客户端流量通过 VMess 协议聚合，减少到 ipv6-proxy 的 TCP 连接数
- **HTTP 入站**：兼容传统 HTTP 代理客户端，无需额外配置
- **统一出口**：所有流量经 ipv6-proxy 随机 IPv6 出口，目标服务器看到不同 IP
