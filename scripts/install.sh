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

# 安装 systemd 服务
log_info "安装 systemd 服务..."
cp /tmp/ipv6-proxy-pool/systemd/ipv6-proxy.service "$SERVICE_FILE"
systemctl daemon-reload

# 提示
log_info "安装完成！"
echo ""
echo "下一步："
echo "1. 编辑配置文件: nano $CONFIG_DIR/config.yaml"
echo "2. 配置本地路由: ip route add local <your-prefix> dev <interface>"
echo "3. 配置 ndppd: cp /tmp/ipv6-proxy-pool/systemd/ndppd.conf.example /etc/ndppd.conf"
echo "4. 启动服务: systemctl enable --now ipv6-proxy"
echo ""
echo "查看日志: journalctl -u ipv6-proxy -f"