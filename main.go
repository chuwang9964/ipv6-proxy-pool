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
	"sync"
	"time"
)

var (
	httpPort  = flag.String("http", "0.0.0.0:53420", "HTTP proxy listen address ")
	socksPort = flag.String("socks", "0.0.0.0:53421", "SOCKS5 proxy listen address")
	prefix    = flag.String("prefix", "240e:6b0:50:0:0:0:2::/112", "IPv6 prefix for outgoing IPs")
	limit     = flag.Int("c", 5000, "max concurrent connections (semaphore limit)")
)

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
		log.Fatalf("invalid prefix: %v", err)
	}

	s := &server{
		network: ipnet,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
		sem:     make(chan struct{}, *limit),
	}

	var wg sync.WaitGroup

	// HTTP CONNECT proxy
	if *httpPort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			http.HandleFunc("/", s.handleHTTP)
			log.Printf("[HTTP] listening on %s", *httpPort)
			if err := http.ListenAndServe(*httpPort, nil); err != nil {
				log.Fatalf("[HTTP] %v", err)
			}
		}()
	}

	// SOCKS5 proxy
	if *socksPort != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("[SOCKS5] listening on %s", *socksPort)
			if err := s.listenSOCKS5(); err != nil {
				log.Fatalf("[SOCKS5] %v", err)
			}
		}()
	}

	wg.Wait()
}

func (s *server) randomIP() net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := make(net.IP, 16)
	copy(ip, s.network.IP.To16())

	ones, _ := s.network.Mask.Size()
	hostBits := 128 - ones
	numBytes := hostBits / 8
	remainderBits := hostBits % 8
	startByte := 16 - numBytes

	for i := startByte; i < 16; i++ {
		ip[i] = byte(s.rnd.Intn(256))
	}
	if remainderBits > 0 && startByte > 0 {
		idx := startByte - 1
		mask := uint8((1 << remainderBits) - 1)
		ip[idx] = (ip[idx] & ^mask) | (byte(s.rnd.Intn(1<<remainderBits)) & mask)
	}
	return ip
}

// ========== HTTP CONNECT ==========

func (s *server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dst := r.Host
	if _, _, err := net.SplitHostPort(dst); err != nil {
		dst += ":443"
	}

	select {
	case s.sem <- struct{}{}:
	default:
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer func() { <-s.sem }()

	// 先 Hijack，再 Dial，避免 ResponseWriter 缓冲污染
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "not supported", http.StatusInternalServerError)
		return
	}

	client, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()

	// 清空 hijack 后的残留数据
	if bufrw.Reader.Buffered() > 0 {
		io.CopyN(io.Discard, bufrw, int64(bufrw.Reader.Buffered()))
	}

	if tc, ok := client.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	src := s.randomIP()
	dialer := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: src, Port: 0},
		Timeout:   30 * time.Second,
	}

	conn, err := dialer.Dial("tcp6", dst)
	if err != nil {
		log.Printf("[HTTP-FAIL] %s via %s: %v", dst, src, err)
		client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	log.Printf("[HTTP-OK] %s via %s", dst, src)

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}

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
			log.Printf("[SOCKS5] accept error: %v", err)
			continue
		}
		go s.handleSOCKS5(conn)
	}
}

func (s *server) handleSOCKS5(conn net.Conn) {
	defer conn.Close()

	select {
	case s.sem <- struct{}{}:
	default:
		log.Printf("[SOCKS5] reject: too many connections")
		return
	}
	defer func() { <-s.sem }()

	if err := s.socks5Handshake(conn); err != nil {
		log.Printf("[SOCKS5] handshake error: %v", err)
		return
	}

	dst, err := s.socks5ParseRequest(conn)
	if err != nil {
		log.Printf("[SOCKS5] request error: %v", err)
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
		log.Printf("[SOCKS5-FAIL] %s via %s: %v", dst, src, err)
		s.socks5Reply(conn, 0x04)
		return
	}
	defer remote.Close()

	log.Printf("[SOCKS5-OK] %s via %s", dst, src)

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
