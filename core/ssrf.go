package core

import (
	"fmt"
	"net"
)

// validateDialTarget 校验目标地址是否安全（SSRF 防护）
func validateDialTarget(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("target address is blocked")
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no DNS records found")
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("target address is blocked")
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	ip = ip.To16()
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// AWS/GCP/Aliyun metadata endpoint
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}

	blockedCIDRs := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "0.0.0.0/8", "169.254.0.0/16",
		"192.0.0.0/24", "192.0.2.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	for _, cidr := range blockedCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
