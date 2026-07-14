package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	httpPort  = flag.String("http", "0.0.0.0:53420", "HTTP proxy listen address")
	socksPort = flag.String("socks", "0.0.0.0:53421", "SOCKS5 proxy listen address")
	prefix    = flag.String("prefix", "240e:6b0:50::/112", "IPv6 prefix for outgoing IPs")
	limit     = flag.Int("c", 5000, "max concurrent connections (semaphore limit)")
	verbose   = flag.Bool("v", false, "enable verbose logging")

	// 日志记录器
	infoLog  *log.Logger
	warnLog  *log.Logger
	errorLog *log.Logger
	debugLog *log.Logger
)

func init() {
	infoLog = log.New(os.Stdout, "[INFO] ", log.LstdFlags|log.Lmsgprefix)
	warnLog = log.New(os.Stdout, "[WARN] ", log.LstdFlags|log.Lmsgprefix)
	errorLog = log.New(os.Stderr, "[ERROR] ", log.LstdFlags|log.Lmsgprefix)
	debugLog = log.New(os.Stdout, "[DEBUG] ", log.LstdFlags|log.Lmsgprefix)
}

// debugf 仅在 verbose 模式下输出调试日志
func debugf(format string, v ...interface{}) {
	if *verbose {
		debugLog.Printf(format, v...)
	}
}

type server struct {
	network *net.IPNet
	rnd     *rand.Rand
	mu      sync.Mutex
	sem     chan struct{}
}

func main() {
	flag.Parse()

	_, ipnet, err := net.ParseCIDR(*prefix)
	if err != nil {
		errorLog.Fatalf("无效的 IPv6 前缀 %q: %v", *prefix, err)
	}

	infoLog.Printf("启动 IPv6 Proxy Pool")
	infoLog.Printf("  IPv6 前缀: %s", ipnet.String())
	infoLog.Printf("  并发限制: %d", *limit)

	s := &server{
		network: ipnet,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
		sem:     make(chan struct{}, *limit),
	}

	var wg sync.WaitGroup

	// HTTP CONNECT proxy - 使用自定义 Server，绕过 DefaultServeMux 的路由限制
	if *httpPort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			httpSrv := &http.Server{
				Addr:    *httpPort,
				Handler: http.HandlerFunc(s.handleHTTP),
			}
			infoLog.Printf("[HTTP] 监听地址: %s", *httpPort)
			if err := httpSrv.ListenAndServe(); err != nil {
				errorLog.Fatalf("[HTTP] 服务启动失败: %v", err)
			}
		}()
	}

	// SOCKS5 proxy
	if *socksPort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			infoLog.Printf("[SOCKS5] 监听地址: %s", *socksPort)
			if err := s.listenSOCKS5(); err != nil {
				errorLog.Fatalf("[SOCKS5] 服务启动失败: %v", err)
			}
		}()
	}

	wg.Wait()
}

// randomIP 生成一个符合前缀范围的随机 IPv6 地址
func (s *server) randomIP() net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := make(net.IP, 16)
	copy(ip, s.network.IP.To16())

	ones, bits := s.network.Mask.Size()
	hostBits := bits - ones // bits 总是 128
	numBytes := hostBits / 8
	remainderBits := hostBits % 8
	startByte := 16 - numBytes

	// 随机填充完整字节部分
	for i := startByte; i < 16; i++ {
		ip[i] = byte(s.rnd.Intn(256))
	}
	// 处理不足一字节的部分（仅当 remainderBits > 0 且 startByte > 0）
	if remainderBits > 0 && startByte > 0 {
		idx := startByte - 1
		mask := uint8((1 << remainderBits) - 1)
		// 保留原有高位，随机低位
		ip[idx] = (ip[idx] & ^mask) | (byte(s.rnd.Intn(1<<remainderBits)) & mask)
	}
	return ip
}

// ========== HTTP CONNECT ==========

func (s *server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	debugf("[HTTP] %s %s (来自: %s)", r.Method, r.URL, r.RemoteAddr)

	if r.Method != http.MethodConnect {
		warnLog.Printf("[HTTP] 拒绝非 CONNECT 请求: %s %s", r.Method, r.URL)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 目标地址：优先使用 r.Host（CONNECT 请求中 r.Host 是 host:port）
	dst := r.Host
	if dst == "" {
		dst = r.URL.Host
	}
	if _, _, err := net.SplitHostPort(dst); err != nil {
		dst += ":443" // 默认端口
	}

	// 并发控制
	select {
	case s.sem <- struct{}{}:
		debugf("[HTTP] 获取信号量成功 (当前: %d/%d)", len(s.sem), cap(s.sem))
	default:
		warnLog.Printf("[HTTP] 连接数超限，拒绝请求: %d/%d", len(s.sem), cap(s.sem))
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer func() { <-s.sem }()

	// Hijack 连接
	hj, ok := w.(http.Hijacker)
	if !ok {
		errorLog.Printf("[HTTP] Hijack 不支持: %s %s", r.Method, r.RemoteAddr)
		http.Error(w, "not supported", http.StatusInternalServerError)
		return
	}
	client, bufrw, err := hj.Hijack()
	if err != nil {
		errorLog.Printf("[HTTP] Hijack 失败: %v", err)
		return
	}
	defer client.Close()

	// 清空可能残留的缓冲数据
	if bufrw.Reader.Buffered() > 0 {
		if _, err := io.CopyN(io.Discard, bufrw, int64(bufrw.Reader.Buffered())); err != nil {
			warnLog.Printf("[HTTP] 清空缓冲数据失败: %v", err)
		}
	}

	if tc, ok := client.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	// 随机源 IP
	src := s.randomIP()
	dialer := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: src, Port: 0},
		Timeout:   30 * time.Second,
	}

	conn, err := dialer.Dial("tcp6", dst)
	if err != nil {
		errorLog.Printf("[HTTP-FAIL] 连接失败: 目标=%s, 源IP=%s, 错误=%v", dst, src, err)
		if _, err := client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n")); err != nil {
			warnLog.Printf("[HTTP-FAIL] 写入 502 响应失败: %v", err)
		}
		return
	}
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	infoLog.Printf("[HTTP-OK] 连接成功: 目标=%s, 源IP=%s", dst, src)

	// 发送成功响应
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		errorLog.Printf("[HTTP-FAIL] 写入 200 响应失败: 目标=%s, 错误=%v", dst, err)
		return
	}

	// 双向拷贝
	var relay sync.WaitGroup
	relay.Add(2)
	go func() {
		io.Copy(conn, client)
		conn.Close()
		relay.Done()
	}()
	go func() {
		io.Copy(client, conn)
		client.Close()
		relay.Done()
	}()
	relay.Wait()
}

// ========== SOCKS5 ==========

func (s *server) listenSOCKS5() error {
	ln, err := net.Listen("tcp", *socksPort)
	if err != nil {
		return err
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			errorLog.Printf("[SOCKS5] Accept 失败: %v", err)
			continue
		}
		go s.handleSOCKS5(conn)
	}
}

func (s *server) handleSOCKS5(conn net.Conn) {
	defer conn.Close()

	select {
	case s.sem <- struct{}{}:
		debugf("[SOCKS5] 获取信号量成功 (当前: %d/%d)", len(s.sem), cap(s.sem))
	default:
		warnLog.Printf("[SOCKS5] 连接数超限，拒绝连接: %d/%d", len(s.sem), cap(s.sem))
		return
	}
	defer func() { <-s.sem }()

	if err := s.socks5Handshake(conn); err != nil {
		errorLog.Printf("[SOCKS5] 握手失败: %v", err)
		return
	}

	dst, err := s.socks5ParseRequest(conn)
	if err != nil {
		errorLog.Printf("[SOCKS5] 请求解析失败: %v", err)
		s.socks5Reply(conn, 0x07)
		return
	}

	src := s.randomIP()
	dialer := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: src, Port: 0},
		Timeout:   30 * time.Second,
	}

	remote, err := dialer.Dial("tcp6", dst)
	if err != nil {
		errorLog.Printf("[SOCKS5-FAIL] 连接失败: 目标=%s, 源IP=%s, 错误=%v", dst, src, err)
		s.socks5Reply(conn, 0x04)
		return
	}
	defer remote.Close()

	infoLog.Printf("[SOCKS5-OK] 连接成功: 目标=%s, 源IP=%s", dst, src)

	if err := s.socks5Reply(conn, 0x00); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		io.Copy(remote, conn)
		remote.Close()
		wg.Done()
	}()
	go func() {
		io.Copy(conn, remote)
		conn.Close()
		wg.Done()
	}()
	wg.Wait()
}

func (s *server) socks5Handshake(conn net.Conn) error {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != 0x05 {
		return errors.New("unsupported socks version")
	}
	nmethods := int(buf[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	_, err := conn.Write([]byte{0x05, 0x00})
	return err
}

func (s *server) socks5ParseRequest(conn net.Conn) (string, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", errors.New("unsupported socks version")
	}
	if buf[1] != 0x01 {
		return "", errors.New("unsupported command")
	}

	var host string
	switch buf[3] {
	case 0x01:
		return "", errors.New("IPv4 not supported")
	case 0x03:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", err
		}
		domain := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		host = string(domain)
	case 0x04:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return "", err
		}
		host = net.IP(ipBuf).String()
	default:
		return "", errors.New("unsupported address type")
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	return fmt.Sprintf("[%s]:%d", host, port), nil
}

func (s *server) socks5Reply(conn net.Conn, rep byte) error {
	_, err := conn.Write([]byte{
		0x05, rep, 0x00, 0x04,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0,
	})
	return err
}
