# IPv6 Proxy Pool

基于 IPv6 /64 或 /112 前缀的随机出口代理池。纯 Go 实现，零外部依赖。

## 核心特性

- **SOCKS5 + HTTP CONNECT** 双协议支持
- **强制 IPv6 出口**：`tcp6` Dial，IPv4-only 目标直接拒绝
- **/112 子网隔离**：同一 /64 下多台服务器互不干扰
- **并发限流**：内置 semaphore，防止资源耗尽
- **与 v2ray/xray 配合**：通过 smux 聚合缓解路由器 conntrack 压力

## 快速开始

```bash
# 安装
curl -sSL https://raw.githubusercontent.com/chuwang9964/ipv6-proxy-pool/main/scripts/install.sh | sudo bash

# 运行
sudo ./ipv6-proxy -http 0.0.0.0:53420 -socks 0.0.0.0:53421 -prefix 240e:6b0:50::/112

# 测试
curl --socks5 127.0.0.1:53421 https://api6.ipify.org 
curl --proxy 127.0.0.1:53420 https://api6.ipify.org
```
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
    inet6 240e:6b0:50::fd5/128 scope global noprefixroute 
       valid_lft forever preferred_lft forever
    inet6 240e:6b0:50:0:e654:e8ff:fe99:c647/64 scope global mngtmpaddr noprefixroute 
       valid_lft forever preferred_lft forever
    inet6 fe80::e654:e8ff:fe99:c647/64 scope link 
       valid_lft forever preferred_lft forever

# 添加本地路由，取前四位/64 ，对应的网口
sudo ip route add local 240e:6b0:50::/64 dev enp2s0
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

    rule 240e:6b0:50::/64 {
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
sudo ./ipv6-proxy -http 0.0.0.0:53420 -socks 0.0.0.0:53421 -prefix 240e:6b0:50::/64 -c 10000
```
4. 测试
```bash
curl --socks5 127.0.0.1:53421 https://api6.ipify.org
curl --proxy 127.0.0.1:53420 https://api6.ipify.org
```
