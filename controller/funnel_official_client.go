package controller

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"tailscale.com/tailcfg"
)

func officialFunnelPorts(cfg FunnelPlatformConfig) []int {
	seen := make(map[int]struct{})
	ports := make([]int, 0, len(cfg.DirectBindPorts))
	for _, port := range cfg.DirectBindPorts {
		if port <= 0 || port == 80 {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func officialServeAvailable(cfg FunnelPlatformConfig) bool {
	_, ok := officialFunnelPortCapability(cfg)
	return ok
}

func officialFunnelAvailable(cfg FunnelPlatformConfig) bool {
	_, ok := officialFunnelPortCapability(cfg)
	return ok
}

func machineCanUseOfficialServe(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) bool {
	if machine == nil || !officialServeAvailable(cfg) {
		return false
	}
	return len(officialCertDomainsForMachine(machine, ipPrefixes, cfg)) > 0
}

func machineCanUseOfficialFunnel(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) bool {
	if machine == nil || !officialFunnelAvailable(cfg) {
		return false
	}
	return len(officialCertDomainsForMachine(machine, ipPrefixes, cfg)) > 0
}

func officialFunnelPortCapability(cfg FunnelPlatformConfig) (tailcfg.NodeCapability, bool) {
	ports := officialFunnelPorts(cfg)
	if len(ports) == 0 {
		return "", false
	}
	if !containsInt(ports, 443) {
		ports = append(ports, 443)
		sort.Ints(ports)
	}

	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}

	return tailcfg.NodeCapability(fmt.Sprintf("%s?ports=%s", tailcfg.CapabilityFunnelPorts, strings.Join(parts, ","))), true
}

func machineNeedsOfficialFunnelWiring(machine *Machine) bool {
	if machine == nil {
		return false
	}
	hostInfo := machine.GetHostInfo()
	return hostInfo.IngressEnabled || hostInfo.WireIngress
}

func machineHasOfficialFunnelIngress(machine *Machine) bool {
	if machine == nil {
		return false
	}
	return machine.GetHostInfo().IngressEnabled
}

func officialFunnelBaseDomainForMachine(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) string {
	if machine == nil {
		return ""
	}
	baseDomain := strings.TrimSuffix(normalizeManagedFQDN(cfg.ManagedBaseDomain), ".")
	if baseDomain != "" {
		return baseDomain
	}
	baseDomain = strings.TrimSuffix(normalizeManagedFQDN(machine.User.Organization.MagicDnsDomain), ".")
	if baseDomain == "" && machine.User.Organization.EnableMagic {
		_, baseDomain = machine.User.GetDNSConfig(ipPrefixes)
		baseDomain = strings.TrimSuffix(baseDomain, ".")
	}
	return baseDomain
}

func officialFunnelDomainForMachine(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) string {
	if machine == nil {
		return ""
	}
	baseDomain := officialFunnelBaseDomainForMachine(machine, ipPrefixes, cfg)
	if baseDomain != "" {
		return normalizeFunnelBaseDomain(machine.GivenName + "." + baseDomain)
	}
	return normalizeFunnelBaseDomain(machine.GivenName)
}

func officialCertDomainsForMachine(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) []string {
	domain := strings.TrimSuffix(officialFunnelDomainForMachine(machine, ipPrefixes, cfg), ".")
	if domain == "" || !strings.Contains(domain, ".") {
		return nil
	}
	return []string{domain}
}

func officialACMEChallengeNameForMachine(machine *Machine, ipPrefixes []netip.Prefix, cfg FunnelPlatformConfig) string {
	domains := officialCertDomainsForMachine(machine, ipPrefixes, cfg)
	if len(domains) == 0 {
		return ""
	}
	return "_acme-challenge." + domains[0]
}

func machinePeerAPIAddress(machine *Machine) (string, bool) {
	if machine == nil {
		return "", false
	}
	hostInfo := machine.GetHostInfo()
	var peerAPI4Port uint16
	var peerAPI6Port uint16
	for _, svc := range hostInfo.Services {
		switch svc.Proto {
		case tailcfg.PeerAPI4:
			if svc.Port > 0 {
				peerAPI4Port = svc.Port
			}
		case tailcfg.PeerAPI6:
			if svc.Port > 0 {
				peerAPI6Port = svc.Port
			}
		}
	}

	if peerAPI6Port > 0 {
		for _, addr := range machine.IPAddresses {
			if addr.Is6() {
				return net.JoinHostPort(addr.String(), strconv.Itoa(int(peerAPI6Port))), true
			}
		}
	}
	if peerAPI4Port > 0 {
		for _, addr := range machine.IPAddresses {
			if addr.Is4() {
				return net.JoinHostPort(addr.String(), strconv.Itoa(int(peerAPI4Port))), true
			}
		}
	}

	return "", false
}

func machineAddressPrefixes(machine *Machine) []netip.Prefix {
	if machine == nil {
		return nil
	}

	prefixes := make([]netip.Prefix, 0, len(machine.IPAddresses))
	for _, addr := range machine.IPAddresses {
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes
}

func (h *Mirage) officialFunnelIngressRulesForMachine(machine *Machine) []tailcfg.FilterRule {
	if h == nil || machine == nil || !machineHasOfficialFunnelIngress(machine) {
		return nil
	}
	if _, ok := officialFunnelPortCapability(h.cfg.FunnelCfg); !ok {
		return nil
	}

	dsts := machineAddressPrefixes(machine)
	if len(dsts) == 0 {
		return nil
	}

	return []tailcfg.FilterRule{{
		SrcIPs: []string{"*"},
		CapGrant: []tailcfg.CapGrant{{
			Dsts: dsts,
			Caps: []tailcfg.PeerCapability{tailcfg.PeerCapabilityIngress},
		}},
	}}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
