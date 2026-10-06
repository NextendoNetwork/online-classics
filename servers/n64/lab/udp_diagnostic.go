package main

import (
	"fmt"
	"log"
	"net"
)

func udpSourceClass(ip net.IP) string {
	if ip.IsLoopback() {
		return "loopback"
	}
	if ip.IsPrivate() {
		return "private"
	}
	return "other"
}

// Opt-in receiver without replies or payload decoding/persistence.
// Listens only on loopback; metadata is limited to 32 datagrams per listener.
func startUDPDiagnostic(address string, logger *log.Logger) (*net.UDPConn, error) {
	return startUDPDiagnosticListener(address, logger, false)
}

// LAN is opt-in and must belong to a local adapter. Binding can suppress the
// OS's port-unreachable response; this is an experiment, not a passive capture.
func startUDPDiagnosticListener(address string, logger *log.Logger, allowLAN bool) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	if !addr.IP.IsLoopback() {
		if !allowLAN {
			return nil, fmt.Errorf("UDP diagnostic requires loopback")
		}
		if _, err := labListenerNetwork(addr.IP); err != nil {
			return nil, err
		}
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	destination := conn.LocalAddr().String()
	logger.Printf("UDP diagnostic: destination=%s receive_only=true", destination)
	go func() {
		buffer := make([]byte, 65535)
		for observed := 0; ; {
			n, peer, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if observed < 32 {
				logger.Printf("UDP diagnostic: destination=%s bytes=%d source_class=%s source_port=%d reply=false", destination, n, udpSourceClass(peer.IP), peer.Port)
				observed++
			}
		}
	}()
	return conn, nil
}

func startSessionUDPDiagnostics(addresses []string, logger *log.Logger) ([]*net.UDPConn, error) {
	var conns []*net.UDPConn
	seen := make(map[string]bool)
	for _, address := range addresses {
		if seen[address] {
			continue
		}
		seen[address] = true
		conn, err := startUDPDiagnosticListener(address, logger, true)
		if err != nil {
			closeUDPDiagnostics(conns)
			return nil, err
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

func closeUDPDiagnostics(conns []*net.UDPConn) {
	for _, conn := range conns {
		conn.Close()
	}
}

func startLoopbackUDPDiagnostics(ports []int, logger *log.Logger) ([]*net.UDPConn, error) {
	var conns []*net.UDPConn
	seen := make(map[int]bool)
	for _, port := range ports {
		if port <= 0 || port > 65535 {
			closeUDPDiagnostics(conns)
			return nil, fmt.Errorf("Invalid diagnostic UDP port")
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		for _, ip := range []string{"127.0.0.1", "127.0.0.2"} {
			conn, err := startUDPDiagnostic(net.JoinHostPort(ip, fmt.Sprint(port)), logger)
			if err != nil {
				closeUDPDiagnostics(conns)
				return nil, err
			}
			conns = append(conns, conn)
		}
	}
	return conns, nil
}
