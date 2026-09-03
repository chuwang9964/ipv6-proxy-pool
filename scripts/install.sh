#!/bin/bash
set -e

# IPv6 Proxy Pool 一键安装脚本
# 适用于 Ubuntu/Debian

REPO="https://github.com/chuwang9964/ipv6-proxy-pool"
INSTALL_DIR="/opt/ipv6-proxy"
CONFIG_DIR="/etc/ipv6-proxy"
SERVICE_FILE="/etc/systemd/system/ipv6-proxy.service"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 root
if [ "$EUID" -ne 0 ]; then
    log_error "请使用 root 权限运行"
    exit 1
fi

# ========== 交互式配置 ==========
echo ""
echo "=== IPv6 Proxy Pool 配置 ==="
echo ""

# 自动检测网口（排除 lo）
DEFAULT_IFACE=$(ip -o link show up | awk -F': ' '!/lo/{print $2}' | head -1)
read -p "网口名称 [$DEFAULT_IFACE]: " IFACE
IFACE=${IFACE:-$DEFAULT_IFACE}

# 自动检测 IPv6 前缀（取第一个全局 /64 地址的前缀）
DEFAULT_PREFIX=$(ip -6 -o addr show dev "$IFACE" scope global | awk '{print $4}' | grep -v '/128' | head -1)
read -p "IPv6 前缀 (如 240e:6b0:50:0:1::/112) [$DEFAULT_PREFIX]: " PREFIX
PREFIX=${PREFIX:-$DEFAULT_PREFIX}

if [ -z "$PREFIX" ]; then
    log_error "未指定 IPv6 前缀，退出"
    exit 1
fi

# 监听端口
read -p "HTTP 代理端口 [53420]: " HTTP_PORT
HTTP_PORT=${HTTP_PORT:-53420}
read -p "SOCKS5 代理端口 [53421]: " SOCKS_PORT
SOCKS_PORT=${SOCKS_PORT:-53421}
read -p "并发上限 [10000]: " CONN_LIMIT
CONN_LIMIT=${CONN_LIMIT:-10000}

echo ""
log_info "配置确认："
echo "  网口:     $IFACE"
echo "  前缀:     $PREFIX"
echo "  HTTP:     0.0.0.0:$HTTP_PORT"
echo "  SOCKS5:   0.0.0.0:$SOCKS_PORT"
echo "  并发上限: $CONN_LIMIT"
echo ""

# 安装依赖
log_info "安装依赖..."
apt-get update
apt-get install -y golang-go git ndppd curl

# 创建目录
log_info "创建目录..."
mkdir -p "$INSTALL_DIR" "$CONFIG_DIR"
chmod 755 "$INSTALL_DIR"

# 克隆或下载代码
log_info "下载代码..."
if command -v git &> /dev/null; then
    git clone "$REPO" /tmp/ipv6-proxy-pool
    cp /tmp/ipv6-proxy-pool/main.go "$INSTALL_DIR/"
    cp /tmp/ipv6-proxy-pool/go.mod "$INSTALL_DIR/"
else
    log_warn "git 未安装，尝试直接下载..."
    curl -L "$REPO/archive/refs/heads/main.tar.gz" -o /tmp/ipv6-proxy-pool.tar.gz
    tar xzf /tmp/ipv6-proxy-pool.tar.gz -C /tmp
    cp /tmp/ipv6-proxy-pool-*/main.go "$INSTALL_DIR/"
    cp /tmp/ipv6-proxy-pool-*/go.mod "$INSTALL_DIR/"
fi

# 编译
log_info "编译..."
cd "$INSTALL_DIR"
go mod tidy
go build -o ipv6-proxy main.go
chmod +x ipv6-proxy

# 复制配置文件
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
    cp /tmp/ipv6-proxy-pool/config.yaml.example "$CONFIG_DIR/config.yaml"
    log_info "配置文件已创建: $CONFIG_DIR/config.yaml"
fi

# 系统参数优化
log_info "配置内核参数..."
cat > /etc/sysctl.d/99-ipv6-proxy.conf <<'EOF'
net.ipv6.ip_nonlocal_bind = 1
net.ipv6.conf.all.forwarding = 1
net.ipv6.neigh.default.gc_thresh1 = 1024
net.ipv6.neigh.default.gc_thresh2 = 4096
net.ipv6.neigh.default.gc_thresh3 = 102400
net.ipv4.tcp_tw_reuse = 1
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_fin_timeout = 15
EOF
sysctl -p /etc/sysctl.d/99-ipv6-proxy.conf

# 绑定 IPv6 地址到接口 + 添加本地路由（两者缺一不可）
log_info "绑定 IPv6 前缀 $PREFIX 到 $IFACE..."
ip addr add "$PREFIX" dev "$IFACE" 2>/dev/null || log_warn "地址已存在，跳过"
ip route add local "$PREFIX" dev "$IFACE" 2>/dev/null || log_warn "路由已存在，跳过"

# 配置 ndppd
log_info "配置 ndppd..."
cat > /etc/ndppd.conf <<EOF
route-ttl 30000

proxy $IFACE {
    router no
    timeout 500
    ttl 30000

    rule $PREFIX {
        static
    }
}
EOF
systemctl enable ndppd
systemctl restart ndppd

# 生成 systemd 服务（自动填入用户配置）
log_info "安装 systemd 服务..."
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=IPv6 Rotating Proxy (HTTP+SOCKS5)
After=network-online.target ndppd.service
Wants=network-online.target ndppd.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/ipv6-proxy

# 启动前绑定 IPv6 前缀到接口 + 添加本地路由（两者缺一不可）
ExecStartPre=/bin/bash -c '/sbin/ip addr add $PREFIX dev $IFACE 2>/dev/null || true; /sbin/ip route add local $PREFIX dev $IFACE 2>/dev/null || true'

ExecStart=/opt/ipv6-proxy/ipv6-proxy \\
    -http 0.0.0.0:$HTTP_PORT \\
    -socks 0.0.0.0:$SOCKS_PORT \\
    -prefix $PREFIX \\
    -c $CONN_LIMIT

Restart=always
RestartSec=5
StartLimitInterval=60s
StartLimitBurst=3

LimitNOFILE=1048576
LimitNPROC=65535
OOMScoreAdjust=-800

StandardOutput=journal
StandardError=journal
SyslogIdentifier=ipv6-proxy

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable ipv6-proxy
systemctl start ipv6-proxy

# 完成
log_info "安装完成！服务已启动"
echo ""
echo "查看状态: systemctl status ipv6-proxy"
echo "查看日志: journalctl -u ipv6-proxy -f"
echo ""
echo "测试:"
echo "  curl -s -x http://127.0.0.1:$HTTP_PORT https://api6.ipify.org"
echo "  curl -s --socks5 127.0.0.1:$SOCKS_PORT https://api6.ipify.org"