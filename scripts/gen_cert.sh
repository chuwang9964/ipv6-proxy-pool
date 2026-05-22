#!/bin/bash
set -e

# 自签证书生成脚本
# 用于 v2ray/xray TLS 配置

CERT_DIR="/usr/local/etc/xray"
DAYS=365

# 支持命令行参数
CN="${1:-$(curl -s -4 ifconfig.me)}"

if [ "$EUID" -ne 0 ]; then
    echo "请使用 root 权限运行"
    exit 1
fi

mkdir -p "$CERT_DIR"

echo "生成自签证书..."
echo "CN (Common Name): $CN"

openssl req -x509 -nodes -days "$DAYS" -newkey rsa:2048 \
    -keyout "$CERT_DIR/key.pem" \
    -out "$CERT_DIR/cert.pem" \
    -subj "/CN=$CN" \
    -addext "subjectAltName=IP:$CN"

chmod 644 "$CERT_DIR/cert.pem"
chmod 600 "$CERT_DIR/key.pem"

echo ""
echo "证书已生成:"
echo "  证书: $CERT_DIR/cert.pem"
echo "  私钥: $CERT_DIR/key.pem"
echo ""
echo "验证:"
openssl x509 -in "$CERT_DIR/cert.pem" -noout -text | grep -E "Subject:|DNS:|IP Address"