package controller

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"tailscale.com/net/tsaddr"
)

type machineRouteDetail struct {
	Prefix            string `json:"prefix"`
	Enabled           bool   `json:"enabled"`
	IsExitNode        bool   `json:"isExitNode"`
	IsVia             bool   `json:"isVia"`
	ViaSiteID         uint32 `json:"viaSiteID,omitempty"`
	ViaOriginalPrefix string `json:"viaOriginalPrefix,omitempty"`
	DisplayLabel      string `json:"displayLabel"`
}

type viaRoutePreview struct {
	OriginalPrefix string `json:"originalPrefix"`
	SiteID         uint32 `json:"siteID"`
	ViaPrefix      string `json:"viaPrefix"`
	Command        string `json:"command"`
}

func decodeViaPrefix(prefix netip.Prefix) (uint32, netip.Prefix, bool) {
	if !prefix.Addr().Is6() || !tsaddr.IsViaPrefix(prefix) {
		return 0, netip.Prefix{}, false
	}

	originalBits := prefix.Bits() - 96
	if originalBits < 0 || originalBits > 32 {
		return 0, netip.Prefix{}, false
	}

	addr16 := prefix.Addr().As16()
	siteID := binary.BigEndian.Uint32(addr16[8:12])
	originalAddr := netip.AddrFrom4([4]byte{addr16[12], addr16[13], addr16[14], addr16[15]})

	return siteID, netip.PrefixFrom(originalAddr, originalBits).Masked(), true
}

func buildMachineRouteDetail(prefix netip.Prefix, enabled bool) machineRouteDetail {
	detail := machineRouteDetail{
		Prefix:       prefix.String(),
		Enabled:      enabled,
		IsExitNode:   prefix == ExitRouteV4 || prefix == ExitRouteV6,
		DisplayLabel: prefix.String(),
	}

	if siteID, originalPrefix, ok := decodeViaPrefix(prefix); ok {
		detail.IsVia = true
		detail.ViaSiteID = siteID
		detail.ViaOriginalPrefix = originalPrefix.String()
		detail.DisplayLabel = fmt.Sprintf("%s (4via6 site %d -> %s)", originalPrefix.String(), siteID, prefix.String())
	}

	return detail
}

func buildViaRoutePreview(prefix netip.Prefix, siteID uint32) (*viaRoutePreview, error) {
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("仅支持 IPv4 子网前缀")
	}
	if prefix.Bits() == 0 {
		return nil, fmt.Errorf("不支持为出口路由生成 4via6 前缀")
	}
	if siteID == 0 {
		return nil, fmt.Errorf("site ID 必须大于 0")
	}

	viaPrefix, err := tsaddr.MapVia(siteID, prefix.Masked())
	if err != nil {
		return nil, err
	}

	return &viaRoutePreview{
		OriginalPrefix: prefix.Masked().String(),
		SiteID:         siteID,
		ViaPrefix:      viaPrefix.String(),
		Command:        fmt.Sprintf("mirage set --advertise-routes=%s", viaPrefix.String()),
	}, nil
}
