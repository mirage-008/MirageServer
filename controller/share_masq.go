package controller

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"
	"strconv"

	"go4.org/netipx"
	"tailscale.com/tailcfg"
)

var (
	sharePeerMasqIPv4Prefix = netip.MustParsePrefix("100.127.0.0/16")
	sharePeerMasqIPv6Prefix = netip.MustParsePrefix("fd7a:115c:a1e0:ffff::/64")
)

func isSharedPeerMasqReservedIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.Is4() {
		return sharePeerMasqIPv4Prefix.Contains(addr)
	}

	return sharePeerMasqIPv6Prefix.Contains(addr)
}

func nextAddrAfterSharedPeerMasqRange(addr netip.Addr) netip.Addr {
	switch {
	case sharePeerMasqIPv4Prefix.Contains(addr):
		return netipx.RangeOfPrefix(sharePeerMasqIPv4Prefix).To().Next()
	case sharePeerMasqIPv6Prefix.Contains(addr):
		return netipx.RangeOfPrefix(sharePeerMasqIPv6Prefix).To().Next()
	default:
		return addr.Next()
	}
}

func sharedPeerMasqAddr(nodeID, peerID int64, wantV6 bool) netip.Addr {
	if wantV6 {
		return sharedPeerMasqAddrV6(nodeID, peerID)
	}

	return sharedPeerMasqAddrV4(nodeID, peerID)
}

func sharedPeerMasqAddrV4(nodeID, peerID int64) netip.Addr {
	base := sharePeerMasqIPv4Prefix.Addr().As4()
	host := 1 + uint32(sharedPeerMasqHash(nodeID, peerID, false)%(1<<16-2))
	base[2] = byte(host >> 8)
	base[3] = byte(host)

	return netip.AddrFrom4(base)
}

func sharedPeerMasqAddrV6(nodeID, peerID int64) netip.Addr {
	base := sharePeerMasqIPv6Prefix.Addr().As16()
	host := sharedPeerMasqHash(nodeID, peerID, true)
	if host == 0 {
		host = 1
	}
	binary.BigEndian.PutUint64(base[8:], host)

	return netip.AddrFrom16(base)
}

func sharedPeerMasqHash(nodeID, peerID int64, wantV6 bool) uint64 {
	hasher := fnv.New64a()
	var input [17]byte
	binary.BigEndian.PutUint64(input[0:8], uint64(nodeID))
	binary.BigEndian.PutUint64(input[8:16], uint64(peerID))
	if wantV6 {
		input[16] = 6
	} else {
		input[16] = 4
	}
	_, _ = hasher.Write(input[:])

	return hasher.Sum64()
}

func machineHasAddressFamily(machine *Machine, wantV6 bool) bool {
	if machine == nil {
		return false
	}
	for _, addr := range machine.IPAddresses {
		if wantV6 && addr.Is6() {
			return true
		}
		if !wantV6 && addr.Is4() {
			return true
		}
	}

	return false
}

func sharedPeerDisplayAddresses(viewer *Machine, peer Machine) []netip.Addr {
	if viewer == nil {
		return peer.IPAddresses
	}

	display := make([]netip.Addr, 0, len(peer.IPAddresses))
	for _, addr := range peer.IPAddresses {
		displayAddr := addr
		if peer.Shared || peer.ShareeNode {
			displayAddr = sharedPeerMasqAddr(peer.ID, viewer.ID, addr.Is6())
		}
		display = append(display, displayAddr)
	}

	return display
}

func applySharedPeerMasquerade(viewer *Machine, peer Machine, node *tailcfg.Node) {
	if viewer == nil || node == nil || (!peer.Shared && !peer.ShareeNode) {
		return
	}

	var (
		peerV4Masq   netip.Addr
		peerV6Masq   netip.Addr
		viewerV4Masq netip.Addr
		viewerV6Masq netip.Addr
	)
	if machineHasAddressFamily(&peer, false) {
		peerV4Masq = sharedPeerMasqAddr(peer.ID, viewer.ID, false)
	}
	if machineHasAddressFamily(&peer, true) {
		peerV6Masq = sharedPeerMasqAddr(peer.ID, viewer.ID, true)
	}
	if machineHasAddressFamily(viewer, false) {
		viewerV4Masq = sharedPeerMasqAddr(viewer.ID, peer.ID, false)
		node.SelfNodeV4MasqAddrForThisPeer = new(netip.Addr)
		*node.SelfNodeV4MasqAddrForThisPeer = viewerV4Masq
	}
	if machineHasAddressFamily(viewer, true) {
		viewerV6Masq = sharedPeerMasqAddr(viewer.ID, peer.ID, true)
		node.SelfNodeV6MasqAddrForThisPeer = new(netip.Addr)
		*node.SelfNodeV6MasqAddrForThisPeer = viewerV6Masq
	}

	for i, prefix := range node.Addresses {
		switch {
		case prefix.Addr().Is4() && peerV4Masq.IsValid():
			node.Addresses[i] = netip.PrefixFrom(peerV4Masq, peerV4Masq.BitLen())
		case prefix.Addr().Is6() && peerV6Masq.IsValid():
			node.Addresses[i] = netip.PrefixFrom(peerV6Masq, peerV6Masq.BitLen())
		}
	}
	for i, prefix := range node.AllowedIPs {
		if prefix.Bits() != prefix.Addr().BitLen() {
			continue
		}
		for _, original := range peer.IPAddresses {
			if prefix.Addr() != original {
				continue
			}
			switch {
			case original.Is4() && peerV4Masq.IsValid():
				node.AllowedIPs[i] = netip.PrefixFrom(peerV4Masq, peerV4Masq.BitLen())
			case original.Is6() && peerV6Masq.IsValid():
				node.AllowedIPs[i] = netip.PrefixFrom(peerV6Masq, peerV6Masq.BitLen())
			}
			break
		}
	}
}

func applySelectedExitNodeProjection(viewer *Machine, peer Machine, node *tailcfg.Node) {
	if viewer == nil || node == nil {
		return
	}
	if viewer.GetHostInfo().ExitNodeID != tailcfg.StableNodeID(strconv.FormatInt(peer.ID, Base10)) {
		return
	}

	filteredAllowed := make([]netip.Prefix, 0, len(node.AllowedIPs))
	for _, prefix := range node.AllowedIPs {
		switch {
		case prefix.Bits() == 0:
			filteredAllowed = append(filteredAllowed, prefix)
		case prefix.Bits() == prefix.Addr().BitLen():
			filteredAllowed = append(filteredAllowed, prefix)
		}
	}
	node.AllowedIPs = filteredAllowed
	node.PrimaryRoutes = nil
}
