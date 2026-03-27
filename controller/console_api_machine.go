package controller

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type machineData struct {
	Address                []string `json:"addresses"`
	AllowedIPs             []string `json:"allowedIPs"`
	ExtraIPs               []string `json:"extraIPs"`
	AdvertisedIPs          []string `json:"advertisedIPs"`
	HasSubnets             bool     `json:"hasSubnets"`
	AdvertisedExitNode     bool     `json:"advertisedExitNode"`
	AllowedExitNode        bool     `json:"allowedExitNode"`
	HasExitNode            bool     `json:"hasExitNode"` //未实现
	AllowedTags            []string `json:"allowedTags"` //未实现
	InvalidTags            []string `json:"invalidTags"` //未实现
	HasTags                bool     `json:"hasTags"`     //？？？移除？
	Endpoints              []string `json:"endpoints"`
	IpnVersion             string   `json:"ipnVersion"` //未实现
	Os                     string   `json:"os"`         //未实现
	Name                   string   `json:"name"`       //未实现
	Fqdn                   string   `json:"fqdn"`       //未实现
	Domain                 string   `json:"domain"`     //未实现
	Created                string   `json:"created"`    //未实现
	Hostname               string   `json:"hostname"`   //未实现
	MachineKey             string   `json:"machineKey"` //未实现
	NodeKey                string   `json:"nodeKey"`    //未实现
	Id                     string   `json:"id"`         //未实现
	StableId               string   `json:"stableId"`   //未实现
	User                   string   `json:"user"`       //未实现
	Creator                string   `json:"creator"`    //未实现
	Expires                string   `json:"expires"`
	NeverExpires           bool     `json:"neverExpires"`
	Authorized             bool     `json:"authorized"`             //未实现
	IsExternal             bool     `json:"isExternal"`             // ？？？        //未实现
	BrokenIPForwarding     bool     `json:"brokenIPForwarding"`     //未实现
	IsEphemeral            bool     `json:"isEphemeral"`            //未实现
	AvailableUpdateVersion string   `json:"availableUpdateVersion"` //未实现
	LastSeen               string   `json:"lastSeen"`               //未实现
	ConnectedToControl     bool     `json:"connectedToControl"`     //未实现
	AutomaticNameMode      bool     `json:"automaticNameMode"`
	TailnetLockKey         string   `json:"tailnetLockKey"`     //未实现
	ShareID                string   `json:"shareID"`            //未实现
	AcceptedShareCount     int      `json:"acceptedShareCount"` //未实现
	ParsedLinuxVersion     string   `json:"parsedLinuxVersion"` //未实现
}

type machineItem struct {
	Id                     string   `json:"id"`                     //done
	StableId               string   `json:"stableId"`               //未实现
	Name                   string   `json:"name"`                   //done
	Fqdn                   string   `json:"fqdn"`                   //未实现
	User                   string   `json:"user"`                   //done
	UserNameHead           string   `json:"usernamehead"`           // TODO
	Addresses              []string `json:"addresses"`              //done
	Os                     string   `json:"os"`                     //done
	Hostname               string   `json:"hostname"`               //done
	IpnVersion             string   `json:"ipnVersion"`             //done
	ConnectedToControl     bool     `json:"connectedToControl"`     //done
	AvailableUpdateVersion string   `json:"availableUpdateVersion"` //未实现
	LastSeen               string   `json:"lastSeen"`               //done
	Created                string   `json:"created"`                //done

	IsExternal         bool                    `json:"isExternal"`
	IsEphemeral        bool                    `json:"isEphemeral"`
	IsSharedOut        bool                    `json:"issharedout"`
	ShareID            string                  `json:"shareID"`
	AcceptedShareCount int                     `json:"acceptedShareCount"`
	ActiveShares       []*machineShareResponse `json:"activeShares"`
	NeverExpires       bool                    `json:"neverExpires"` //done

	AllowedIPs         []string `json:"allowedIPs"`
	ExtraIPs           []string `json:"extraIPs"`
	AdvertisedIPs      []string `json:"advertisedIPs"`
	HasSubnets         bool     `json:"hasSubnets"`
	AdvertisedExitNode bool     `json:"advertisedExitNode"`
	AllowedExitNode    bool     `json:"allowedExitNode"`
	AllowedTags        []string `json:"allowedTags"`
	InvalidTags        []string `json:"invalidTags"`
	HasTags            bool     `json:"hasTags"`

	Expires time.Time `json:"expires"`

	ExpiryDesc string `json:"expirydesc"`

	Endpoints         []string `json:"endpoints"`
	AutomaticNameMode bool     `json:"automaticNameMode"`
}

func IsUpdateAvailable(cur, latest string) bool {
	curV := strings.Split(strings.Split(cur, "-")[0], ".")
	latestV := strings.Split(strings.Split(latest, "-")[0], ".")
	for i := 0; i < len(latestV); i++ {
		curInt, _ := strconv.Atoi(curV[i])
		latestInt, _ := strconv.Atoi(latestV[i])
		if curInt < latestInt {
			return true
		}
	}
	return false
}

// 控制台获取设备信息列表的API
func (h *Mirage) ConsoleMachinesAPI(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	orgMachines, err := h.ListVisibleMachinesByOrgID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "查询用户节点列表失败", nil)
		return
	}

	tz, _ := time.LoadLocation("Asia/Shanghai")
	mlist := make([]machineItem, 0, len(orgMachines))
	for _, machine := range orgMachines {
		userNameHead := ""
		if machine.User.Display_Name != "" {
			userNameHead = string([]rune(machine.User.Display_Name)[0])
		} else if machine.User.Name != "" {
			userNameHead = string([]rune(machine.User.Name)[0])
		}

		lastSeen := ""
		if machine.LastSeen != nil {
			lastSeen = machine.LastSeen.In(tz).Format("2006年01月02日 15:04:05")
		}

		expires := time.Time{}
		neverExpires := true
		if machine.Expiry != nil {
			expires = *machine.Expiry
			neverExpires = machine.Expiry.IsZero()
		}

		tmpMachine := machineItem{
			Id:                 strconv.FormatInt(machine.ID, 10),
			Name:               machine.GivenName,
			User:               machine.User.Name,
			UserNameHead:       userNameHead,
			Os:                 machine.HostInfo.OS,
			Hostname:           machine.HostInfo.Hostname,
			IpnVersion:         machine.HostInfo.IPNVersion,
			Created:            machine.CreatedAt.In(tz).Format("2006年01月02日 15:04:05"),
			LastSeen:           lastSeen,
			ConnectedToControl: machine.isOnline(),
			AllowedTags:        machine.ForcedTags,
			InvalidTags:        []string{},
			HasTags:            machine.ForcedTags != nil && len(machine.ForcedTags) > 0,
			IsExternal:         machine.Shared || machine.User.OrganizationID != user.OrganizationID,
			IsEphemeral:        machine.isEphemeral(),
			NeverExpires:       neverExpires,
			Expires:            expires,
			Endpoints:          machine.Endpoints,
			AutomaticNameMode:  machine.AutoGenName,
		}

		if !tmpMachine.IsExternal {
			shares, err := h.ListMachineSharesBySourceMachine(machine.ID)
			if err != nil {
				h.doAPIResponse(w, "查询设备分享信息失败", nil)
				return
			}
			for i := range shares {
				share := &shares[i]
				if tmpMachine.ShareID == "" {
					tmpMachine.ShareID = share.StableID
				}
				tmpMachine.IsSharedOut = true
				tmpMachine.ActiveShares = append(tmpMachine.ActiveShares, h.buildMachineShareResponse(share))
				if share.Status == MachineShareStatusAccepted {
					tmpMachine.AcceptedShareCount++
				}
			}
		}

		switch machine.HostInfo.OS {
		case "linux":
			if IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.Linux.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.Linux.Version, "-")[0]
			}
		case "windows":
			if IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.Win.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.Win.Version, "-")[0]
			}
		case "macOS":
			if h.cfg.ClientVersion.MacStore.Version != "" && IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.MacStore.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.MacStore.Version, "-")[0]
			} else if IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.MacTestFlight.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.MacTestFlight.Version, "-")[0]
			}
		case "iOS":
			if h.cfg.ClientVersion.IOSStore.Version != "" && IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.IOSStore.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.IOSStore.Version, "-")[0]
			} else if IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.IOSTestFlight.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.IOSTestFlight.Version, "-")[0]
			}
		case "android":
			if IsUpdateAvailable(machine.HostInfo.IPNVersion, h.cfg.ClientVersion.Android.Version) {
				tmpMachine.AvailableUpdateVersion = strings.Split(h.cfg.ClientVersion.Android.Version, "-")[0]
			}
		}

		if machine.User.Organization.EnableMagic {
			tmpMachine.Fqdn = machine.GivenName + "." + machine.User.Organization.MagicDnsDomain
		}
		machineRoutes, err := h.GetMachineRoutes(&machine)
		if err != nil {
			h.doAPIResponse(w, "查询设备路由失败", nil)
			return
		}
		for _, route := range machineRoutes {
			if route.isExitRoute() {
				if route.Advertised {
					tmpMachine.AdvertisedExitNode = true
					if route.Enabled {
						tmpMachine.AllowedExitNode = true
					}
				}
			} else if route.Advertised {
				tmpMachine.HasSubnets = true
				routeV := netip.Prefix(route.Prefix).String()
				tmpMachine.AdvertisedIPs = append(tmpMachine.AdvertisedIPs, routeV)
				if route.Enabled {
					tmpMachine.AllowedIPs = append(tmpMachine.AllowedIPs, routeV)
				} else {
					tmpMachine.ExtraIPs = append(tmpMachine.ExtraIPs, routeV)
				}
			}
		}

		if !tmpMachine.NeverExpires {
			tmpMachine.ExpiryDesc = convExpiryToStr(time.Until(expires))
		}
		switch {
		case len(machine.IPAddresses) >= 2 && machine.IPAddresses[0].Is4():
			tmpMachine.Addresses = []string{machine.IPAddresses[0].String(), machine.IPAddresses[1].String()}
		case len(machine.IPAddresses) >= 2 && machine.IPAddresses[1].Is4():
			tmpMachine.Addresses = []string{machine.IPAddresses[1].String(), machine.IPAddresses[0].String()}
		default:
			tmpMachine.Addresses = machine.IPAddresses.ToStringSlice()
		}
		mlist = append(mlist, tmpMachine)
	}

	h.doAPIResponse(w, "", struct {
		Machines []machineItem `json:"machines"`
	}{
		Machines: mlist,
	})
}

type MachineDebugInfo struct {
	MappingVariesByDestIP bool                `json:"mappingVariesByDestIP"`
	HairPinning           bool                `json:"hairPinning"`
	IPv6                  bool                `json:"ipv6"`
	UDP                   bool                `json:"udp"`
	UPnP                  bool                `json:"upnp"`
	PMP                   bool                `json:"pmp"`
	PCP                   bool                `json:"pcp"`
	Latency               map[string]*Latency `json:"latency"` // "derp区域编号"-结构体
}
type Latency struct {
	RegionName string  `json:"regionName"`
	Preferred  bool    `json:"preferred"`
	LatencyMs  float64 `json:"latencyMs"`
}

func machineNeverExpires(machine *Machine) bool {
	return machine == nil || machine.Expiry == nil || machine.Expiry.IsZero()
}

func mapShareErrorMessage(err error, creating bool) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrMachineShareTargetInvalid):
		if creating {
			return "目标身份无效"
		}
		return "分享信息无效"
	case errors.Is(err, ErrMachineShareTargetMismatch):
		return "登录身份与分享目标不匹配"
	case errors.Is(err, ErrMachineShareTargetAlreadyInOrg):
		return "目标身份已在当前组织中"
	case errors.Is(err, ErrMachineShareAlreadyAccepted):
		return "分享已被接受"
	case errors.Is(err, ErrMachineShareAlreadyRejected):
		return "分享已被拒绝"
	case errors.Is(err, ErrMachineShareAlreadyRevoked):
		return "分享已被撤销"
	case errors.Is(err, ErrMachineShareNotFound):
		return "分享不存在"
	default:
		return err.Error()
	}
}

type machineShareResponse struct {
	Id              string     `json:"id"`
	StableId        string     `json:"stableId"`
	ShareToken      string     `json:"shareToken"`
	ShareURL        string     `json:"shareURL"`
	TargetIdentity  string     `json:"targetIdentity"`
	Status          string     `json:"status"`
	SourceMachineID string     `json:"sourceMachineID"`
	TargetOrgID     string     `json:"targetOrgID"`
	AcceptedAt      *time.Time `json:"acceptedAt"`
	RejectedAt      *time.Time `json:"rejectedAt"`
	RevokedAt       *time.Time `json:"revokedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
}

func (h *Mirage) buildMachineShareResponse(share *MachineShare) *machineShareResponse {
	if share == nil {
		return nil
	}

	res := &machineShareResponse{
		Id:              strconv.FormatInt(share.ID, 10),
		StableId:        share.StableID,
		ShareToken:      share.ShareToken,
		ShareURL:        h.buildMachineShareURL(share.ShareToken),
		TargetIdentity:  share.TargetIdentity,
		Status:          share.Status,
		SourceMachineID: strconv.FormatInt(share.SourceMachineID, 10),
		AcceptedAt:      share.AcceptedAt,
		RejectedAt:      share.RejectedAt,
		RevokedAt:       share.RevokedAt,
		CreatedAt:       share.CreatedAt.UTC(),
	}
	if share.TargetOrgID != 0 {
		res.TargetOrgID = strconv.FormatInt(share.TargetOrgID, 10)
	}

	return res
}

func (h *Mirage) resolveShareIDFromRequest(reqData map[string]interface{}) (int64, error) {
	if shareID, ok := parseRequestInt64(reqData, "shareID", "shareId", "id"); ok {
		return shareID, nil
	}
	stableID := parseRequestString(reqData, "shareStableID", "shareStableId", "stableId")
	if stableID == "" {
		return 0, ErrMachineShareNotFound
	}
	share, err := h.GetMachineShareByStableID(stableID)
	if err != nil {
		return 0, err
	}

	return share.ID, nil
}

func (h *Mirage) getVisibleMachineForOrg(machineID int64, orgID int64) (*Machine, error) {
	machine, err := h.GetMachineByID(machineID)
	if err != nil {
		return nil, err
	}
	visible, err := h.IsMachineVisibleToOrg(machine, orgID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrMachineNotFound
	}

	return machine, nil
}

func (m *Mirage) ConsoleMachineDebugAPI(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := m.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		m.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	targetMIPStr := r.URL.Query().Get("ip")
	if targetMIPStr == "" {
		m.doAPIResponse(w, "用户查询IP为空", nil)
		return
	}
	targetMIP, err := netip.ParseAddr(targetMIPStr)
	if err != nil {
		m.doAPIResponse(w, "用户请求IP解析失败", nil)
		return
	}
	targetMachine := m.GetMachineByIP(targetMIP)
	if targetMachine == nil {
		m.doAPIResponse(w, "组织内无此设备", nil)
		return
	}
	visible, err := m.IsMachineVisibleToOrg(targetMachine, user.OrganizationID)
	if err != nil {
		m.doAPIResponse(w, "查询设备权限失败", nil)
		return
	}
	if !visible {
		m.doAPIResponse(w, "组织内无此设备", nil)
		return
	}
	if targetMachine.User.OrganizationID != user.OrganizationID {
		targetMachine.Shared = true
	}
	var (
		mappingVariesByDestIP bool
		ipv6                  bool
		udp                   bool
		upnp                  bool
		pmp                   bool
		pcp                   bool
	)
	if targetMachine.HostInfo.NetInfo != nil {
		mappingVariesByDestIP = targetMachine.HostInfo.NetInfo.MappingVariesByDestIP.EqualBool(true)
		ipv6 = targetMachine.HostInfo.NetInfo.WorkingIPv6.EqualBool(true)
		udp = targetMachine.HostInfo.NetInfo.WorkingUDP.EqualBool(true)
		upnp = targetMachine.HostInfo.NetInfo.UPnP.EqualBool(true)
		pmp = targetMachine.HostInfo.NetInfo.PMP.EqualBool(true)
		pcp = targetMachine.HostInfo.NetInfo.PCP.EqualBool(true)
	}
	resData := &MachineDebugInfo{
		MappingVariesByDestIP: mappingVariesByDestIP,
		HairPinning:           false,
		IPv6:                  ipv6,
		UDP:                   udp,
		UPnP:                  upnp,
		PMP:                   pmp,
		PCP:                   pcp,
		Latency:               make(map[string]*Latency),
	}
	derpMap, err := m.LoadOrgDERPs(targetMachine.User.OrganizationID)
	if err != nil {
		m.doAPIResponse(w, "获取组织中继信息失败", nil)
		return
	}

	if targetMachine.HostInfo.NetInfo != nil && targetMachine.HostInfo.NetInfo.PreferredDERP != 0 {
		for derpname, latency := range targetMachine.HostInfo.NetInfo.DERPLatency {
			ipver := strings.Split(derpname, "-")[1]
			derpRegionIDStr := strings.Split(derpname, "-")[0]
			derpRegionID, _ := strconv.Atoi(derpRegionIDStr)

			if resData.Latency[derpRegionIDStr] == nil {
				resData.Latency[derpRegionIDStr] = &Latency{
					RegionName: derpMap.Regions[derpRegionID].RegionName,
					Preferred:  targetMachine.HostInfo.NetInfo.PreferredDERP == derpRegionID,
				}
			}

			if ipver == "v4" {
				if peerlatency, ok := targetMachine.HostInfo.NetInfo.DERPLatency[derpRegionIDStr+"-v6"]; ok {
					if latency < peerlatency {
						resData.Latency[derpRegionIDStr].LatencyMs = latency * 1000
					}
				} else {
					resData.Latency[derpRegionIDStr].LatencyMs = latency * 1000
				}
			} else {
				if peerlatency, ok := targetMachine.HostInfo.NetInfo.DERPLatency[derpRegionIDStr+"-v4"]; ok {
					if latency < peerlatency {
						resData.Latency[derpRegionIDStr].LatencyMs = latency * 1000
					}
				} else {
					resData.Latency[derpRegionIDStr].LatencyMs = latency * 1000
				}
			}
		}
	}
	m.doAPIResponse(w, "", resData)
}

func (h *Mirage) ConsoleMachinesUpdateAPI(
	writer http.ResponseWriter,
	req *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(writer, req)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(writer, "用户信息核对失败:"+err.Error(), nil)
		return
	}
	if err := req.ParseForm(); err != nil {
		h.doAPIResponse(writer, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	reqData := make(map[string]interface{})
	if err := json.NewDecoder(req.Body).Decode(&reqData); err != nil && err.Error() != "EOF" {
		h.doAPIResponse(writer, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	reqState := parseRequestString(reqData, "state", "action")
	if reqState == "" {
		h.doAPIResponse(writer, "用户请求state解析失败", nil)
		return
	}

	switch reqState {
	case "accept_share", "accept-share":
		shareToken := parseRequestString(reqData, "shareToken", "token")
		share, err := h.AcceptMachineShareByToken(shareToken, user)
		if err != nil {
			h.doAPIResponse(writer, mapShareErrorMessage(err, false), nil)
			return
		}
		h.doAPIResponse(writer, "", h.buildMachineShareResponse(share))
		return
	case "reject_share", "reject-share":
		shareToken := parseRequestString(reqData, "shareToken", "token")
		share, err := h.RejectMachineShareByToken(shareToken, user)
		if err != nil {
			h.doAPIResponse(writer, mapShareErrorMessage(err, false), nil)
			return
		}
		h.doAPIResponse(writer, "", h.buildMachineShareResponse(share))
		return
	case "revoke_share", "revoke-share":
		shareID, err := h.resolveShareIDFromRequest(reqData)
		if err != nil {
			h.doAPIResponse(writer, mapShareErrorMessage(err, false), nil)
			return
		}
		if err := h.RevokeMachineShare(shareID, user.OrganizationID); err != nil {
			h.doAPIResponse(writer, mapShareErrorMessage(err, false), nil)
			return
		}
		h.doAPIResponse(writer, "", nil)
		return
	}

	machineID, ok := parseRequestInt64(reqData, "mid", "machineID", "machineId")
	if !ok {
		h.doAPIResponse(writer, "用户请求mid解析失败", nil)
		return
	}
	toUpdateMachine, err := h.getVisibleMachineForOrg(machineID, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(writer, "查询用户设备失败", nil)
		return
	}
	if toUpdateMachine.User.OrganizationID != user.OrganizationID {
		toUpdateMachine.Shared = true
	}

	switch reqState {
	case "create_share", "share", "create-share":
		if toUpdateMachine.Shared {
			h.doAPIResponse(writer, "外部共享设备不支持此操作", nil)
			return
		}
		targetIdentity := parseRequestString(reqData, "targetIdentity", "targetUser", "targetUserName", "targetLoginName")
		share, err := h.CreateMachineShare(toUpdateMachine, user, targetIdentity)
		if err != nil {
			h.doAPIResponse(writer, mapShareErrorMessage(err, true), nil)
			return
		}
		h.doAPIResponse(writer, "", h.buildMachineShareResponse(share))
		return
	}

	if toUpdateMachine.Shared {
		h.doAPIResponse(writer, "外部共享设备不支持此操作", nil)
		return
	}

	switch reqState {
	case "set-expires":
		msg, err := h.setMachineExpiry(toUpdateMachine)
		if err != nil {
			h.doAPIResponse(writer, msg, nil)
		} else {
			resData := machineData{
				NeverExpires: machineNeverExpires(toUpdateMachine),
				Expires:      msg,
			}
			h.doAPIResponse(writer, "", resData)
		}
	case "rename-node":
		newName := parseRequestString(reqData, "nodeName")
		msg, _, err := h.setMachineName(toUpdateMachine, newName)
		if err != nil {
			h.doAPIResponse(writer, msg, nil)
		} else {
			resData := machineData{
				AutomaticNameMode: toUpdateMachine.AutoGenName,
				Name:              toUpdateMachine.GivenName,
				Hostname:          toUpdateMachine.Hostname,
				NeverExpires:      machineNeverExpires(toUpdateMachine),
				Expires:           msg,
			}
			h.doAPIResponse(writer, "", resData)
		}
	case "set-route-settings":
		allowedIPsInterface, _ := reqData["allowedIPs"].([]interface{})
		allowExitNode, _ := reqData["allowedExitNode"].(bool)

		allowedIPs := make([]string, 0, len(allowedIPsInterface))
		for _, ip := range allowedIPsInterface {
			if ipStr, ok := ip.(string); ok {
				allowedIPs = append(allowedIPs, ipStr)
			}
		}

		msg, err := h.setMachineSubnet(toUpdateMachine, allowExitNode, allowedIPs)
		if err != nil {
			h.doAPIResponse(writer, msg, nil)
			return
		}

		resData := machineData{
			AutomaticNameMode: toUpdateMachine.AutoGenName,
			Name:              toUpdateMachine.GivenName,
			Hostname:          toUpdateMachine.Hostname,
			NeverExpires:      machineNeverExpires(toUpdateMachine),
			Expires:           msg,
		}
		machineRoutes, err := h.GetMachineRoutes(toUpdateMachine)
		if err != nil {
			h.doAPIResponse(writer, "查询设备路由失败", nil)
			return
		}
		for _, route := range machineRoutes {
			if route.isExitRoute() {
				if route.Advertised {
					resData.AdvertisedExitNode = true
					if route.Enabled {
						resData.AllowedExitNode = true
					}
				}
			} else if route.Advertised {
				resData.HasSubnets = true
				routeV := netip.Prefix(route.Prefix).String()
				resData.AdvertisedIPs = append(resData.AdvertisedIPs, routeV)
				if route.Enabled {
					resData.AllowedIPs = append(resData.AllowedIPs, routeV)
				} else {
					resData.ExtraIPs = append(resData.ExtraIPs, routeV)
				}
			}
		}
		h.doAPIResponse(writer, "", resData)
	case "set-tags":
		reqTags, _ := reqData["tags"].([]interface{})
		setTags := make([]string, 0, len(reqTags))
		for _, tag := range reqTags {
			if tagStr, ok := tag.(string); ok {
				setTags = append(setTags, tagStr)
			}
		}
		msg, err := h.setMachineTags(toUpdateMachine, setTags)
		if err != nil {
			h.doAPIResponse(writer, msg, nil)
		} else {
			invalidTags := []string{}
			allowedTags := []string{}
			org, err := h.GetOrgnaizationByID(user.OrganizationID)
			if err != nil {
				h.doAPIResponse(writer, msg, nil)
				return
			}
			for _, tag := range setTags {
				if _, ok := org.AclPolicy.TagOwners[tag]; ok {
					allowedTags = append(allowedTags, tag)
				} else {
					invalidTags = append(invalidTags, tag)
				}
			}
			resData := machineData{
				AutomaticNameMode: toUpdateMachine.AutoGenName,
				Name:              toUpdateMachine.GivenName,
				Hostname:          toUpdateMachine.Hostname,
				NeverExpires:      machineNeverExpires(toUpdateMachine),
				Expires:           msg,
				HasTags:           len(setTags) > 0,
				AllowedTags:       allowedTags,
				InvalidTags:       invalidTags,
			}
			h.doAPIResponse(writer, "", resData)
		}
	default:
		h.doAPIResponse(writer, "未知设备操作", nil)
	}
}

// 删除设备API
func (h *Mirage) ConsoleRemoveMachineAPI(
	writer http.ResponseWriter,
	req *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(writer, req)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(writer, "用户信息核对失败:"+err.Error(), nil)
		return
	}
	err = req.ParseForm()
	if err != nil {
		h.doAPIResponse(writer, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	reqData := make(map[string]string)
	json.NewDecoder(req.Body).Decode(&reqData)
	wantRemoveID := reqData["mid"]
	machineID, err := strconv.ParseInt(wantRemoveID, 10, 64)
	if err != nil {
		h.doAPIResponse(writer, "用户请求mid处理失败", nil)
		return
	}

	machine, err := h.getVisibleMachineForOrg(machineID, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(writer, "未找到目标设备", nil)
		return
	}
	if machine.User.OrganizationID != user.OrganizationID {
		h.doAPIResponse(writer, "外部共享设备不支持此操作", nil)
		return
	}

	err = h.HardDeleteMachine(machine)
	if err != nil {
		h.doAPIResponse(writer, "用户设备删除失败:"+err.Error(), nil)
		return
	}
	h.NotifyNaviOrgNodesChange(user.OrganizationID, "", machine.NodeKey)

	h.doAPIResponse(writer, "", nil)
}

// 切换设备密钥是否禁用过期
func (h *Mirage) setMachineExpiry(machine *Machine) (string, error) {
	if (*machine.Expiry != time.Time{}) {
		err := h.RefreshMachine(machine, time.Time{})
		if err != nil {
			return "设备密钥过期禁用失败", err
		} else {
			return "", err
		}
	} else {
		expiryDuration := time.Hour * 24 * time.Duration(machine.User.Organization.ExpiryDuration)
		newExpiry := time.Now().Add(expiryDuration)
		err := h.RefreshMachine(machine, newExpiry)
		if err != nil {
			return "设备密钥过期启用失败", err
		} else {
			return convExpiryToStr(expiryDuration), nil
		}
	}
}

// 三个返回值：msg、nowName、err
func (h *Mirage) setMachineName(machine *Machine, newName string) (string, string, error) {
	newGiveName, err := h.setAutoGenName(machine, newName)
	if err != nil {
		return "设置主机名失败", "", err
	}
	return "", newGiveName, nil
}

func (h *Mirage) setMachineTags(machine *Machine, tags []string) (string, error) {
	err := h.SetTags(machine, tags)
	if err != nil {
		return "设置设备标签失败", err
	}
	return "", nil
}

func (h *Mirage) setMachineSubnet(machine *Machine, ExitNodeEnable bool, allowedIPs []string) (string, error) {
	machineRoutes, err := h.GetMachineRoutes(machine)
	if err != nil {
		return "获取设备路由设置失败", err
	}
	for _, r := range machineRoutes {
		if r.isExitRoute() {
			if ExitNodeEnable {
				err = h.EnableRoute(uint64(r.ID))
			} else {
				err = h.DisableRoute(uint64(r.ID))
			}
			if err != nil {
				return "设置设备出口节点状态失败", err
			}
		} else {
			err = h.DisableRoute(uint64(r.ID))
			if err != nil {
				return "设置设备出口节点状态失败", err
			}
		}
	}
	err = h.enableRoutes(machine, allowedIPs...)
	if err != nil {
		return "设置设备子网路由状态失败", err
	}
	return "", nil
}
