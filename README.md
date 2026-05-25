# IPv6 Proxy Pool

基于 IPv6 /64 或 /112 前缀的随机出口代理池。纯 Go 实现，零外部依赖。

## 核心特性

- **SOCKS5 + HTTP CONNECT** 双协议支持
- **强制 IPv6 出口**：`tcp6` Dial，IPv4-only 目标直接拒绝
- **/112 子网隔离**：同一 /64 下多台服务器互不干扰
- **并发限流**：内置 semaphore，防止资源耗尽
- **与 v2ray/xray 配合**：通过 smux 聚合缓解路由器 conntrack 压力

# 手动部署

1. 系统配置
```bash
#  内核参数
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

# 查看你的ipv6地址 
yxproxy@yxproxy:~$ ip addr
enp2s0: <BROADCAST,MULTICAST,ALLMULTI,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether e4:54:e8:99:c6:47 brd ff:ff:ff:ff:ff:ff
    inet 192.168.1.250/24 brd 192.168.1.255 scope global enp1s0
       valid_lft forever preferred_lft forever
    inet6 240e:7b1:50::fd5/128 scope global noprefixroute 
       valid_lft forever preferred_lft forever

# 添加本地路由，取前四位/64 ，对应的网口
sudo ip route add local 240e:7b1:50::/64 dev enp2s0
```

2. 安装ndppd 对应的网口enp2s0 对应的ipv6网段
```bash
sudo apt install -y ndppd
sudo tee /etc/ndppd.conf <<'EOF'
route-ttl 30000

proxy enp2s0 {
    router no
    timeout 500
    ttl 30000

    rule 240e:7b1:50::/64 {
        static
    }
}
EOF
sudo systemctl enable ndppd
sudo systemctl restart ndppd
```

3. 编译运行
```bash
# 1. 安装 Go（如果还没有）
wget https://go.dev/dl/go1.22.3.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.3.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 2. 编译
sudo mkdir -p /opt/ipv6-proxy
cd /opt/ipv6-proxy
sudo /usr/local/go/bin/go build -o ipv6-proxy main.go
sudo chmod +x /opt/ipv6-proxy/ipv6-proxy
sudo ./ipv6-proxy -http 0.0.0.0:53420 -socks 0.0.0.0:53421 -prefix 240e:7b1:50::/64 -c 10000
```
4. 测试
```bash
curl --socks5 127.0.0.1:53421 https://api6.ipify.org
curl --proxy 127.0.0.1:53420 https://api6.ipify.org
```
5. 添加系统服务
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
# 如果路由器之前炸过，建议先设 3000-5000 观察
ExecStart=/opt/ipv6-proxy/ipv6-proxy \
    -http 0.0.0.0:53420 \
    -socks 0.0.0.0:53421 \
    -prefix 240e:7b1:50::/64 \
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
6.一些踩坑
```bash
HTTP 代理的问题是每个 HTTP 请求新建一个 TCP，路由器 conntrack 几万条后爆炸,所以大量请求的时候优先使用socks5端口，也可以结合v2ray的vmess协议。
    核心思路：v2ray/xray 做"协议前端"（加密 + 多路复用），go 代理只做"IPv6 出口"（随机源地址）。这样路由器 WAN 侧只看到少量 TLS 长连接，而不是海量 HTTP 短连接。
```
