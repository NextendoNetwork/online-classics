package main

import (
	"errors"
	"net"
)

// Listen only on loopback or a private IPv4 assigned to this machine.
// Returns the adapter's actual subnet to restrict ICE peers to Wi-Fi/LAN.
func labListenerNetwork(ip net.IP) (*net.IPNet, error) {
	if ip != nil && ip.IsLoopback() {
		if v4 := ip.To4(); v4 != nil {
			return &net.IPNet{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}, nil
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
	}
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return nil, errors.New("Use loopback or a private IPv4 assigned to this machine")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if subnet, ok := address.(*net.IPNet); ok && subnet.IP.Equal(ip) {
			return &net.IPNet{IP: ip.Mask(subnet.Mask), Mask: subnet.Mask}, nil
		}
	}
	return nil, errors.New("The private IP is not assigned to an adapter on this machine")
}

func labPeerAllowed(subnet *net.IPNet, ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || (subnet != nil && subnet.Contains(ip)))
}
