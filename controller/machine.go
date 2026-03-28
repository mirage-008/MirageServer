package controller

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const (
	ErrMachineNotFound                  = Error("machine not found")
	ErrMachineRouteIsNotAvailable       = Error("route is not available on machine")
	ErrMachineAddressesInvalid          = Error("failed to parse machine addresses")
	ErrMachineIPAddressInvalid          = Error("machine IP address is invalid")
	ErrMachineIPAddressUnavailable      = Error("machine IP address is already in use")
	ErrMachineIPAddressOutOfRange       = Error("machine IP address is outside configured prefixes")
	ErrMachineIPAddressDuplicatePrefix  = Error("machine IP address prefix was specified more than once")
	ErrMachineNotFoundRegistrationCache = Error(
		"machine not found in registration cache",
	)
	ErrCouldNotConvertMachineInterface = Error("failed to convert machine interface")
	ErrHostnameTooLong                 = Error("Hostname too long")
	ErrDifferentRegisteredUser         = Error(
		"machine was previously registered with a different user",
	)
	MachineGivenNameHashLength = 8
	MachineGivenNameTrimSize   = 2
)

const (
	maxHostnameLength = 255
)

// Machine is a Mirage client.
type Machine struct {
	ID          int64  `gorm:"primary_key;unique;not null"`
	MachineKey  string `gorm:"type:varchar(64);"`
	NodeKey     string
	DiscoKey    string
	IPAddresses MachineAddresses

	// Hostname represents the name given by the Tailscale
	// client during registration
	Hostname string

	// Givenname represents either:
	// a DNS normalized version of Hostname
	// a valid name set by the User
	//
	// GivenName is the name used in all DNS related
	// parts of mirage.
	GivenName   string `gorm:"type:varchar(63)"`
	AutoGenName bool   `gorm:"default:true"`
	UserID      int64
	User        User `gorm:"foreignKey:UserID"`

	RegisterMethod string

	ForcedTags StringList

	// TODO(kradalby): This seems like irrelevant information?
	AuthKeyID uint
	AuthKey   *PreAuthKey `gorm:"foreignKey:AuthKeyID"`

	LastSeen             *time.Time
	LastSuccessfulUpdate *time.Time
	Expiry               *time.Time

	HostInfo            HostInfo
	Endpoints           StringList
	DataPlaneAuditLogID string

	CreatedAt time.Time
	UpdatedAt time.Time

	Shared     bool `gorm:"-"`
	ShareeNode bool `gorm:"-"`
}

func (machine *Machine) BeforeCreate(tx *gorm.DB) error {
	if machine.ID == 0 {
		flakeID, err := snowflake.NewNode(1)
		if err != nil {
			return err
		}
		id := flakeID.Generate().Int64()
		machine.ID = id
	}
	return nil
}

type (
	Machines  []Machine
	MachinesP []*Machine
)

type MachineAddresses []netip.Addr

func (ma MachineAddresses) ToStringSlice() []string {
	strSlice := make([]string, 0, len(ma))
	for _, addr := range ma {
		strSlice = append(strSlice, addr.String())
	}

	return strSlice
}

func (ma *MachineAddresses) Scan(destination interface{}) error {
	switch value := destination.(type) {
	case string:
		addresses := strings.Split(value, ",")
		*ma = (*ma)[:0]
		for _, addr := range addresses {
			if len(addr) < 1 {
				continue
			}
			parsed, err := netip.ParseAddr(addr)
			if err != nil {
				return err
			}
			*ma = append(*ma, parsed)
		}

		return nil

	default:
		return fmt.Errorf("%w: unexpected data type %T", ErrMachineAddressesInvalid, destination)
	}
}

// Value return json value, implement driver.Valuer interface.
func (ma MachineAddresses) Value() (driver.Value, error) {
	addresses := strings.Join(ma.ToStringSlice(), ",")

	return addresses, nil
}

func (h *Mirage) addressPrefixIndex(addr netip.Addr) (int, bool) {
	for i, prefix := range h.cfg.IPPrefixes {
		if prefix.Contains(addr) {
			return i, true
		}
	}

	return -1, false
}

func (h *Mirage) validateMachineIPInPrefix(addr netip.Addr, prefix netip.Prefix) error {
	if !prefix.Contains(addr) {
		return ErrMachineIPAddressOutOfRange
	}
	network, broadcast := GetIPPrefixEndpoints(prefix)
	switch {
	case addr == netip.Addr{}:
		return ErrMachineIPAddressInvalid
	case addr.IsLoopback():
		return ErrMachineIPAddressInvalid
	case addr == network:
		return ErrMachineIPAddressInvalid
	case addr == broadcast:
		return ErrMachineIPAddressInvalid
	default:
		return nil
	}
}

func (h *Mirage) machineAddressesByPrefix(machine *Machine) map[int]netip.Addr {
	byPrefix := make(map[int]netip.Addr)
	if machine == nil {
		return byPrefix
	}
	for _, addr := range machine.IPAddresses {
		if idx, ok := h.addressPrefixIndex(addr); ok {
			byPrefix[idx] = addr
		}
	}

	return byPrefix
}

func (h *Mirage) buildUpdatedMachineAddresses(machine *Machine, rawAddresses []string) (MachineAddresses, error) {
	if machine == nil {
		return nil, ErrMachineNotFound
	}

	updatedByPrefix := h.machineAddressesByPrefix(machine)
	explicitByPrefix := make(map[int]netip.Addr)
	sawExplicitAddress := false
	for _, raw := range rawAddresses {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		sawExplicitAddress = true
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, ErrMachineIPAddressInvalid
		}
		prefixIndex, ok := h.addressPrefixIndex(addr)
		if !ok {
			return nil, ErrMachineIPAddressOutOfRange
		}
		if err := h.validateMachineIPInPrefix(addr, h.cfg.IPPrefixes[prefixIndex]); err != nil {
			return nil, err
		}
		if existing, exists := explicitByPrefix[prefixIndex]; exists && existing != addr {
			return nil, ErrMachineIPAddressDuplicatePrefix
		}
		explicitByPrefix[prefixIndex] = addr
		updatedByPrefix[prefixIndex] = addr
	}
	if !sawExplicitAddress && len(updatedByPrefix) == 0 {
		return nil, ErrMachineIPAddressInvalid
	}

	usedIPs, err := h.getUsedIPsExcludingMachine(machine.ID)
	if err != nil {
		return nil, err
	}

	updated := make(MachineAddresses, 0, len(updatedByPrefix))
	for i := range h.cfg.IPPrefixes {
		addr, ok := updatedByPrefix[i]
		if !ok {
			continue
		}
		if usedIPs.Contains(addr) {
			return nil, ErrMachineIPAddressUnavailable
		}
		updated = append(updated, addr)
	}
	if len(updated) == 0 {
		return nil, ErrMachineIPAddressInvalid
	}

	return updated, nil
}

func (h *Mirage) SetMachineAddresses(machine *Machine, rawAddresses []string) error {
	if machine == nil {
		return ErrMachineNotFound
	}

	h.ipAllocationMutex.Lock()
	defer h.ipAllocationMutex.Unlock()

	updatedAddresses, err := h.buildUpdatedMachineAddresses(machine, rawAddresses)
	if err != nil {
		return err
	}

	machine.IPAddresses = updatedAddresses
	if err := h.db.Save(machine).Error; err != nil {
		return err
	}

	connectedOrgIDs, err := h.ListShareConnectedOrgIDs(machine.User.OrganizationID)
	if err != nil || len(connectedOrgIDs) == 0 {
		h.setOrgLastStateChangeToNow(machine.User.OrganizationID)
		return nil
	}
	h.setOrgLastStateChangeToNow(connectedOrgIDs...)

	return nil
}

// isExpired returns whether the machine registration has expired.
func (machine Machine) isExpired() bool {
	// If Expiry is not set, the client has not indicated that
	// it wants an expiry time, it is therefor considered
	// to mean "not expired"
	if machine.Expiry == nil || machine.Expiry.IsZero() {
		return false
	}

	return time.Now().UTC().After(*machine.Expiry)
}

// isOnline returns if the machine is connected to Mirage.
// This is really a naive implementation, as we don't really see
// if there is a working connection between the client and the server.
func (machine *Machine) isOnline() bool {
	if machine.LastSeen == nil {
		return false
	}

	if machine.isExpired() {
		return false
	}

	return machine.LastSeen.After(time.Now().Add(-keepAliveInterval))
}

// isEphemeral returns if the machine is registered as an Ephemeral node.
// https://tailscale.com/kb/1111/ephemeral-nodes/
func (machine *Machine) isEphemeral() bool {
	return machine.AuthKey != nil && machine.AuthKey.Ephemeral
}

func containsAddresses(inputs []string, addrs []string) bool {
	for _, addr := range addrs {
		if containsAddress(inputs, addr) {
			return true
		}
	}

	return false
}

func containsAddress(inputs []string, addr string) bool {
	parsedAddr, err := netip.ParseAddr(addr)
	parsed := err == nil
	for _, input := range inputs {
		if input == "*" || input == addr {
			return true
		}
		if !parsed {
			continue
		}
		if prefix, err := netip.ParsePrefix(input); err == nil && prefix.Contains(parsedAddr) {
			return true
		}
		if inputAddr, err := netip.ParseAddr(input); err == nil && inputAddr == parsedAddr {
			return true
		}
	}

	return false
}

func inputsOverlapPrefix(inputs []string, target netip.Prefix) bool {
	for _, input := range inputs {
		if input == "*" {
			return true
		}
		if prefix, err := netip.ParsePrefix(input); err == nil {
			if prefix.Overlaps(target) || target.Overlaps(prefix) {
				return true
			}
			continue
		}
		if addr, err := netip.ParseAddr(input); err == nil && target.Contains(addr) {
			return true
		}
	}

	return false
}

func inputsOverlapPrefixes(inputs []string, targets []netip.Prefix) bool {
	for _, target := range targets {
		if inputsOverlapPrefix(inputs, target) {
			return true
		}
	}

	return false
}

// matchSourceAndDestinationWithRule.
func matchSourceAndDestinationWithRule(
	ruleSources []string,
	ruleDestinations []string,
	source []string,
	destination []string,
) bool {
	return containsAddresses(ruleSources, source) &&
		containsAddresses(ruleDestinations, destination)
}

func hasExitRoutes(prefixes []netip.Prefix) bool {
	var hasV4, hasV6 bool
	for _, prefix := range prefixes {
		switch prefix {
		case ExitRouteV4:
			hasV4 = true
		case ExitRouteV6:
			hasV6 = true
		}
	}

	return hasV4 && hasV6
}

func (h *Mirage) machineHasEnabledExitRoutes(machine *Machine) bool {
	if machine == nil {
		return false
	}

	enabledRoutes, err := h.GetEnabledRoutes(machine)
	if err != nil {
		log.Warn().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not get enabled routes for exit node detection")

		return false
	}

	return hasExitRoutes(enabledRoutes)
}

func enabledSubnetRoutes(prefixes []netip.Prefix) []netip.Prefix {
	routes := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix == ExitRouteV4 || prefix == ExitRouteV6 {
			continue
		}
		routes = append(routes, prefix)
	}

	return routes
}

func (h *Mirage) machineEnabledSubnetRoutes(machine *Machine) []netip.Prefix {
	if machine == nil {
		return nil
	}

	enabledRoutes, err := h.GetEnabledRoutes(machine)
	if err != nil {
		log.Warn().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not get enabled routes for subnet detection")

		return nil
	}

	return enabledSubnetRoutes(enabledRoutes)
}

func (h *Mirage) machineCanAccessInternet(machines []Machine, machine *Machine, aclPolicy *ACLPolicy) bool {
	if machine == nil || aclPolicy == nil {
		return false
	}

	machineIPs := machine.IPAddresses.ToStringSlice()
	for _, acl := range aclPolicy.ACLs {
		if acl.Action != "accept" {
			continue
		}
		hasInternetDestination := false
		for _, dest := range acl.Destinations {
			if isAutoGroupInternetDestination(dest) {
				hasInternetDestination = true
				break
			}
		}
		if !hasInternetDestination {
			continue
		}
		for _, src := range acl.Sources {
			srcIPs, err := h.generateACLPolicySrc(machines, machine.UserID, *aclPolicy, src, h.cfg.OIDC.StripEmaildomain)
			if err != nil {
				continue
			}
			if containsAddresses(srcIPs, machineIPs) {
				return true
			}
		}
	}

	return false
}

// getFilteredByACLPeerss should return the list of peers authorized to be accessed from machine.
func (h *Mirage) getFilteredByACLPeers(
	machines []Machine,
	rules []tailcfg.FilterRule,
	aclPolicy *ACLPolicy,
	enableSelf bool,
	machine *Machine,
) (Machines, []tailcfg.NodeID) {
	log.Trace().
		Str("machine", machine.Hostname).
		Msg("Finding peers filtered by ACLs")

	peers := make(map[int64]Machine)
	var invalidNodeIDs []tailcfg.NodeID
	// Aclfilter peers here. We are itering through machines in all users and search through the computed aclRules
	// for match between rule SrcIPs and DstPorts. If the rule is a match we allow the machine to be viewable.
	machineIPs := machine.IPAddresses.ToStringSlice()

	machineCanUseInternet := h.machineCanAccessInternet(machines, machine, aclPolicy)
	machineIsEnabledExitNode := h.machineHasEnabledExitRoutes(machine)
	machineEnabledSubnetRoutes := h.machineEnabledSubnetRoutes(machine)

	for _, peer := range machines {
		if peer.ID == machine.ID {
			continue
		}
		if peer.Shared || peer.ShareeNode {
			peers[peer.ID] = peer
			continue
		}
		// 处理self情况:如果启用了self且没有tag,直接加入peer列表
		if enableSelf && peer.UserID == machine.UserID && len(peer.ForcedTags) == 0 && len(machine.ForcedTags) == 0 {
			peers[peer.ID] = peer
			continue
		}

		peerIPs := peer.IPAddresses.ToStringSlice()
		authorized := false
		peerIsEnabledExitNode := h.machineHasEnabledExitRoutes(&peer)
		peerEnabledSubnetRoutes := h.machineEnabledSubnetRoutes(&peer)
		if machineCanUseInternet && peerIsEnabledExitNode {
			authorized = true
		}
		if !authorized && machineIsEnabledExitNode && h.machineCanAccessInternet(machines, &peer, aclPolicy) {
			authorized = true
		}
		for _, rule := range rules {
			var dst []string
			for _, d := range rule.DstPorts {
				dst = append(dst, d.IP)
			}
			if !authorized && len(peerEnabledSubnetRoutes) > 0 && containsAddresses(rule.SrcIPs, machineIPs) && inputsOverlapPrefixes(dst, peerEnabledSubnetRoutes) {
				authorized = true
			}
			if !authorized && len(machineEnabledSubnetRoutes) > 0 && containsAddresses(rule.SrcIPs, peerIPs) && inputsOverlapPrefixes(dst, machineEnabledSubnetRoutes) {
				authorized = true
			}
			if authorized {
				break
			}
			var starInSrc, starInDst bool
			starInSrc = containsAddresses(rule.SrcIPs, []string{"*"})
			starInDst = containsAddresses(dst, []string{"*"})

			if (starInSrc && starInDst) ||
				(starInSrc && containsAddresses(dst, machineIPs)) ||
				(starInDst && containsAddresses(rule.SrcIPs, machineIPs)) ||
				(starInSrc && containsAddresses(dst, peerIPs)) ||
				(starInDst && containsAddresses(rule.SrcIPs, peerIPs)) {
				authorized = true
				break
			}
			if !starInSrc && !starInDst &&
				(matchSourceAndDestinationWithRule(
					rule.SrcIPs,
					dst,
					peerIPs,
					machineIPs,
				) ||
					matchSourceAndDestinationWithRule(
						rule.SrcIPs,
						dst,
						machineIPs,
						peerIPs,
					)) {
				authorized = true
				break
			}
		}
		if authorized {
			peers[peer.ID] = peer
		} else {
			invalidNodeIDs = append(invalidNodeIDs, tailcfg.NodeID(peer.ID))
		}
	}
	authorizedPeers := make([]Machine, 0, len(peers))
	for _, m := range peers {
		authorizedPeers = append(authorizedPeers, m)
	}
	sort.Slice(
		authorizedPeers,
		func(i, j int) bool { return authorizedPeers[i].ID < authorizedPeers[j].ID },
	)

	log.Trace().
		Str("machine", machine.Hostname).
		Msgf("Found some machines: %v", machines)

	return authorizedPeers, invalidNodeIDs
}

func (h *Mirage) ListPeers(machine *Machine) (Machines, error) {
	log.Trace().
		Str("machine", machine.Hostname).
		Msg("Finding direct peers")

	machines := Machines{}
	orgId := machine.User.OrganizationID
	if orgId == 0 {
		return h.ListMachines()
	}
	userIds := []int64{}
	h.db.Model(&User{}).Where(&User{
		OrganizationID: orgId,
	}).Select("id").Find(&userIds)
	if len(userIds) != 0 {
		scopeFunc := func(tx *gorm.DB) *gorm.DB {
			if len(userIds) == 1 {
				return tx.Where("user_id = ?", userIds[0])
			}
			return tx.Where("user_id in ?", userIds)
		}
		if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Scopes(scopeFunc).Where("node_key <> ?", machine.NodeKey).Find(&machines).Error; err != nil {
			log.Error().Err(err).Msg("Error accessing db")
			return Machines{}, err
		}
	}

	sharePeers, err := h.ListSharePeersForMachine(machine)
	if err != nil {
		return Machines{}, err
	}
	merged := mergeMachines(machines, sharePeers)
	peers := make(Machines, 0, len(merged))
	for _, peer := range merged {
		if peer.NodeKey == machine.NodeKey {
			continue
		}
		peers = append(peers, peer)
	}

	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })

	log.Trace().
		Str("machine", machine.Hostname).
		Msgf("Found peers: %s", peers.String())

	return peers, nil
}

func (h *Mirage) getPeers(machine *Machine, enableSelf bool) (Machines, []tailcfg.NodeID, error) {
	var peers Machines
	var invalidNodeIDs []tailcfg.NodeID
	var err error

	org := &machine.User.Organization
	// If ACLs rules are defined, filter visible host list with the ACLs
	// else use the classic user scope
	if machine.User.Organization.AclPolicy != nil {
		var machines []Machine
		machines, err = h.ListPeers(machine)
		if err != nil {
			log.Error().Err(err).Msg("Error retrieving list of machines")

			return Machines{}, []tailcfg.NodeID{}, err
		}
		machines = mergeMachines(machines, []Machine{*machine})
		peers, invalidNodeIDs = h.getFilteredByACLPeers(machines, org.AclRules, org.AclPolicy, enableSelf, machine)
	} else {
		peers, err = h.ListPeers(machine)
		if err != nil {
			log.Error().
				Caller().
				Err(err).
				Msg("Cannot fetch peers")

			return Machines{}, []tailcfg.NodeID{}, err
		}
		invalidNodeIDs = []tailcfg.NodeID{}
	}

	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })

	log.Trace().
		Str("machine", machine.Hostname).
		Msgf("Found total peers: %s", peers.String())

	return peers, invalidNodeIDs, nil
}

func (h *Mirage) getValidPeers(machine *Machine, enableSelf bool) (Machines, []tailcfg.NodeID, error) {
	validPeers := make(Machines, 0)

	peers, nodeIDs, err := h.getPeers(machine, enableSelf)
	if err != nil {
		return Machines{}, []tailcfg.NodeID{}, err
	}

	for _, peer := range peers {
		if !peer.isExpired() {
			validPeers = append(validPeers, peer)
		}
	}

	return validPeers, nodeIDs, nil
}

func (h *Mirage) ListMachines() ([]Machine, error) {
	machines := []Machine{}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Find(&machines).Error; err != nil {
		return nil, err
	}

	return machines, nil
}

func (h *Mirage) ListMachinesByGivenName(givenName string) ([]Machine, error) {
	machines := []Machine{}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Where("given_name = ?", givenName).Find(&machines).Error; err != nil {
		return nil, err
	}

	return machines, nil
}

func (h *Mirage) ListMachinesByOrgID(orgId int64) ([]Machine, error) {
	if orgId == 0 {
		return h.ListMachines()
	}
	machines := []Machine{}
	userIds := []int64{}
	h.db.Model(&User{}).Where(&User{
		OrganizationID: orgId,
	}).Select("id").Find(&userIds)
	if len(userIds) == 0 {
		return machines, nil
	}
	scopeFunc := func(tx *gorm.DB) *gorm.DB {
		if len(userIds) == 1 {
			return tx.Where("user_id = ?", userIds[0])
		} else {
			return tx.Where("user_id in ?", userIds)
		}
	}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Scopes(scopeFunc).Find(&machines).Error; err != nil {
		return nil, err
	}

	return machines, nil
}

// cgao6: 根据MachineKey和组织
// 情况1：不同组织的机器无所谓
// 情况2：同组织内，不允许同名，除非：同设备（MKey）且同用户
func (h *Mirage) GenMachineName(
	referName string,
	userId int64,
	orgId int64,
	machineKey string,
) string {
	giveName := referName
	suffix := 1
	m, err := h.GetOrgMachineByGivenName(giveName, orgId)
	if err == nil && (m.MachineKey != machineKey || m.UserID != userId) {
		giveName = giveName + "-" + strconv.Itoa(suffix)
		m, err = h.GetOrgMachineByGivenName(giveName, orgId)
	}
	for err == nil && (m.MachineKey != machineKey || m.UserID != userId) {
		giveName = strings.TrimSuffix(giveName, "-"+strconv.Itoa(suffix))
		suffix++
		giveName = giveName + "-" + strconv.Itoa(suffix)
		m, err = h.GetOrgMachineByGivenName(giveName, orgId)
	}

	return giveName
}

func (h *Mirage) GetOrgMachineByGivenName(
	giveName string, orgId int64,
) (*Machine, error) {
	machine := Machine{}
	userIds := []int64{}
	h.db.Model(&User{}).Where(&User{
		OrganizationID: orgId,
	}).Select("id").Find(&userIds)
	if len(userIds) == 0 {
		return &machine, nil
	}
	scopeFunc := func(tx *gorm.DB) *gorm.DB {
		if len(userIds) == 1 {
			return tx.Where("user_id = ? AND given_name = ?", userIds[0], giveName)
		} else {
			return tx.Where("user_id in ? AND given_name = ?", userIds, giveName)
		}
	}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Scopes(scopeFunc).First(&machine).Error; err != nil {
		return nil, err
	}

	return &machine, nil
}

// cgao6: 根据Machine GivenName和用户查询机器
func (h *Mirage) GetUserMachineByGivenName(
	givenName string, uid tailcfg.UserID,
) (*Machine, error) {
	machine := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").First(&machine, "given_name = ? AND user_id = ?",
		givenName,
		uid); result.Error != nil {
		return nil, result.Error
	}

	return &machine, nil
}

// cgao6: 根据MachineKey和用户查询机器
func (h *Mirage) GetUserMachineByMachineKey(
	machineKey key.MachinePublic, uid tailcfg.UserID,
) (*Machine, error) {
	machine := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").First(&machine, "machine_key = ? AND user_id = ?",
		MachinePublicKeyStripPrefix(machineKey),
		uid); result.Error != nil {
		return nil, result.Error
	}

	return &machine, nil
}

// cgao6
func (h *Mirage) GetMachineByIP(ip netip.Addr) *Machine {
	machines, err := h.ListMachines()
	if err != nil {
		return nil
	}
	for _, m := range machines {
		for _, mip := range m.IPAddresses.ToStringSlice() {
			if mip == ip.String() {
				return &m
			}
		}
	}
	return nil
}
func (h *Mirage) GetMachinesInPrefix(ip netip.Prefix) []Machine {
	out := []Machine{}
	machines, err := h.ListMachines()
	if err != nil {
		return out
	}
	for _, m := range machines {
		for _, mip := range m.IPAddresses.ToStringSlice() {
			trueip, err := netip.ParseAddr(mip)
			if err != nil {
				return out
			}
			if ip.Contains(trueip) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// cgao6
// GetMachine finds a Machine by user and backendlogid and returns the Machine struct.
func (h *Mirage) GetMachineNSBLID(userID int64, backendlogid string) (*Machine, error) {
	machines, err := h.ListMachinesByUser(userID)
	if err != nil {
		return nil, err
	}

	for _, m := range machines {
		if m.HostInfo.BackendLogID == backendlogid {
			return &m, nil
		}
	}

	return nil, ErrMachineNotFound
}

// GetMachine finds a Machine by name and user and returns the Machine struct.
func (h *Mirage) GetMachine(userID int64, name string) (*Machine, error) {
	machines, err := h.ListMachinesByUser(userID)
	if err != nil {
		return nil, err
	}

	for _, m := range machines {
		if m.Hostname == name {
			return &m, nil
		}
	}

	return nil, ErrMachineNotFound
}

// GetMachineByGivenName finds a Machine by given name and user and returns the Machine struct.
func (h *Mirage) GetMachineByGivenName(userID int64, givenName string) (*Machine, error) {
	machines, err := h.ListMachinesByUser(userID)
	if err != nil {
		return nil, err
	}

	for _, m := range machines {
		if m.GivenName == givenName {
			return &m, nil
		}
	}

	return nil, ErrMachineNotFound
}

// GetMachineByID finds a Machine by ID and returns the Machine struct.
func (h *Mirage) GetMachineByID(id int64) (*Machine, error) {
	m := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").Find(&Machine{ID: id}).First(&m); result.Error != nil {
		return nil, result.Error
	}

	return &m, nil
}

// GetMachineOrgID finds a Machine's Org.
func (h *Mirage) GetMachineOrgByID(machine *Machine) (*Organization, error) {
	user := User{}
	err := h.db.Where("id = ?", machine.UserID).Take(&user).Error
	if err != nil {
		return nil, err
	}
	org := Organization{}
	err = h.db.Where("id = ?", user.OrganizationID).Take(&org).Error
	if err != nil {
		return nil, err
	}
	return &org, nil
}

// GetMachineByMachineKey finds a Machine by its MachineKey and returns the Machine struct.
func (h *Mirage) GetMachineByMachineKey(
	machineKey key.MachinePublic,
) (*Machine, error) {
	m := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").First(&m, "machine_key = ?", MachinePublicKeyStripPrefix(machineKey)); result.Error != nil {
		return nil, result.Error
	}

	return &m, nil
}

// GetMachineByNodeKey finds a Machine by its current NodeKey.
func (h *Mirage) GetMachineByNodeKey(
	nodeKey key.NodePublic,
) (*Machine, error) {
	machine := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").First(&machine, "node_key = ?",
		NodePublicKeyStripPrefix(nodeKey)); result.Error != nil {
		return nil, result.Error
	}

	return &machine, nil
}

// GetMachineByAnyNodeKey finds a Machine by its MachineKey, its current NodeKey or the old one, and returns the Machine struct.
func (h *Mirage) GetMachineByAnyKey(
	machineKey key.MachinePublic, nodeKey key.NodePublic, oldNodeKey key.NodePublic,
) (*Machine, error) {
	machine := Machine{}
	if result := h.db.Preload("AuthKey").Preload("User").Preload("User.Organization").First(&machine, "machine_key = ? OR node_key = ? OR node_key = ?",
		MachinePublicKeyStripPrefix(machineKey),
		NodePublicKeyStripPrefix(nodeKey),
		NodePublicKeyStripPrefix(oldNodeKey)); result.Error != nil {
		return nil, result.Error
	}

	return &machine, nil
}

// UpdateMachineFromDatabase takes a Machine struct pointer (typically already loaded from database
// and updates it with the latest data from the database.
func (h *Mirage) UpdateMachineFromDatabase(machine *Machine) error {
	if result := h.db.Find(machine).Preload("AuthKey").Preload("User").Preload("User.Organization").First(&machine); result.Error != nil {
		return result.Error
	}

	return nil
}

// SetTags takes a Machine struct pointer and update the forced tags.
func (h *Mirage) SetTags(machine *Machine, tags []string) error {
	/*
		org, err := h.GetMachineOrgByID(machine)
		if err != nil {
			return fmt.Errorf("failed to update tags for machine in the database: %w", err)
		}
	*/
	newTags := []string{}
	for _, tag := range tags {
		if !contains(newTags, tag) {
			newTags = append(newTags, tag)
		}
	}
	machine.ForcedTags = newTags
	/*
		if _, err := h.UpdateACLRulesOfOrg(org, &machine.User, machine); err != nil && !errors.Is(err, errEmptyPolicy) {
			return err
		}
	*/
	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	if err := h.db.Save(machine).Error; err != nil {
		return fmt.Errorf("failed to update tags for machine in the database: %w", err)
	}

	return nil
}

// ExpireMachine takes a Machine struct and sets the expire field to now.
func (h *Mirage) ExpireMachine(machine *Machine) error {
	now := time.Now()
	machine.Expiry = &now
	machine.DiscoKey = ""

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	if err := h.db.Save(machine).Error; err != nil {
		return fmt.Errorf("failed to expire machine in the database: %w", err)
	}

	return nil
}

// setAutoGenName can set whether a machine should use hostname as its given name
// (will generated if there's already same hostname node). will return new givenname when success.
func (h *Mirage) setAutoGenName(machine *Machine, newName string) (string, error) {
	isAutoGen := false
	if newName == "" {
		isAutoGen = true
	}
	if !(machine.AutoGenName && machine.GivenName == newName) {
		if isAutoGen {
			if machine.AutoGenName {
				return machine.GivenName, nil
			}
			machine.GivenName = h.GenMachineName(machine.Hostname, machine.UserID, machine.User.OrganizationID, machine.MachineKey)
		} else {
			_, err := h.GetOrgMachineByGivenName(newName, machine.User.OrganizationID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return "", fmt.Errorf("fail to check whether new name already exist: %w", err)
			} else if err == nil {
				return machine.GivenName, nil
			}
			err = h.RenameMachine(machine, newName)
			if err != nil {
				return "", err
			}
		}
	}
	machine.AutoGenName = isAutoGen

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)
	if err := h.db.Save(machine).Error; err != nil {
		return "", fmt.Errorf("failed to save setAutoGen machine in the database: %w", err)
	}

	return machine.GivenName, nil
}

// RenameMachine takes a Machine struct and a new GivenName for the machines
// and renames it.
func (h *Mirage) RenameMachine(machine *Machine, newName string) error {
	err := CheckForFQDNRules(
		newName,
	)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "RenameMachine").
			Str("machine", machine.Hostname).
			Str("newName", newName).
			Err(err)

		return err
	}
	machine.GivenName = newName

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	if err := h.db.Save(machine).Error; err != nil {
		return fmt.Errorf("failed to rename machine in the database: %w", err)
	}

	return nil
}

// RefreshMachine takes a Machine struct and sets the expire field to now.
func (h *Mirage) RefreshMachine(machine *Machine, expiry time.Time) error {
	now := time.Now()

	machine.LastSuccessfulUpdate = &now
	machine.Expiry = &expiry

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	if err := h.db.Save(machine).Error; err != nil {
		return fmt.Errorf(
			"failed to refresh machine (update expiration) in the database: %w",
			err,
		)
	}

	return nil
}

// RestructMachine takes a Machine struct and sets its new keys.
func (h *Mirage) RestructMachine(machine *Machine, expiry time.Time) error {
	now := time.Now()

	machine.LastSuccessfulUpdate = &now
	machine.Expiry = &expiry

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	if err := h.db.Save(machine).Error; err != nil {
		return fmt.Errorf(
			"failed to refresh machine (update expiration) in the database: %w",
			err,
		)
	}

	return nil
}

// DeleteMachine softs deletes a Machine from the database.
func (h *Mirage) DeleteMachine(machine *Machine) error {
	if err := h.db.Delete(&machine).Error; err != nil {
		return err
	}

	return nil
}

func (h *Mirage) TouchMachine(machine *Machine) error {
	return h.db.Updates(Machine{
		ID:                   machine.ID,
		LastSeen:             machine.LastSeen,
		LastSuccessfulUpdate: machine.LastSuccessfulUpdate,
	}).Error
}

// HardDeleteMachine hard deletes a Machine from the database.
func (h *Mirage) HardDeleteMachine(machine *Machine) error {
	h.ipAllocationMutex.Lock()
	defer h.ipAllocationMutex.Unlock()

	connectedOrgIDs, err := h.ListShareConnectedOrgIDs(machine.User.OrganizationID)
	if err != nil {
		connectedOrgIDs = []int64{machine.User.OrganizationID}
	}
	// delete routes of this machine
	h.db.Where(&Route{MachineID: machine.ID}).Delete(&Route{})
	h.db.Where("source_machine_id = ?", machine.ID).Delete(&MachineShare{})
	if err := h.db.Unscoped().Delete(&machine).Error; err != nil {
		return err
	}

	h.setOrgLastStateChangeToNow(connectedOrgIDs...)
	return nil
}

// GetHostInfo returns a Hostinfo struct for the machine.
func (machine *Machine) GetHostInfo() tailcfg.Hostinfo {
	return tailcfg.Hostinfo(machine.HostInfo)
}

func (h *Mirage) isOutdated(machine *Machine) bool {
	if err := h.UpdateMachineFromDatabase(machine); err != nil {
		// It does not seem meaningful to propagate this error as the end result
		// will have to be that the machine has to be considered outdated.
		return true
	}

	// Get the last update from all mirage users to compare with our nodes
	// last update.
	// TODO(kradalby): Only request updates from users where we can talk to nodes
	// This would mostly be for a bit of performance, and can be calculated based on
	// ACLs.
	lastChange := h.getOrgLastStateChange(machine.User.OrganizationID)
	lastUpdate := machine.CreatedAt
	if machine.LastSuccessfulUpdate != nil {
		lastUpdate = *machine.LastSuccessfulUpdate
	}
	log.Trace().
		Str("machine", machine.Hostname).
		Time("last_successful_update", lastChange).
		Time("last_state_change", lastUpdate).
		Msgf("Checking if %s is missing updates", machine.Hostname)

	return lastUpdate.Before(lastChange)
}

func (machine Machine) String() string {
	return machine.Hostname
}

func (machines Machines) String() string {
	temp := make([]string, len(machines))

	for index, machine := range machines {
		temp[index] = machine.Hostname
	}

	return fmt.Sprintf("[ %s ](%d)", strings.Join(temp, ", "), len(temp))
}

// TODO(kradalby): Remove when we have generics...
func (machines MachinesP) String() string {
	temp := make([]string, len(machines))

	for index, machine := range machines {
		temp[index] = machine.Hostname
	}

	return fmt.Sprintf("[ %s ](%d)", strings.Join(temp, ", "), len(temp))
}

func (h *Mirage) toNodes(
	machines Machines,
	// baseDomain string,
	// dnsConfig *tailcfg.DNSConfig,
) ([]*tailcfg.Node, error) {
	nodes := make([]*tailcfg.Node, len(machines))

	for index, machine := range machines {
		node, err := h.toNode(machine, machine.Shared) //, baseDomain, dnsConfig)
		if err != nil {
			return nil, err
		}

		nodes[index] = node
	}

	return nodes, nil
}

// toNode converts a Machine into a Tailscale Node.
// Shared peers keep their enabled exit-node and subnet-route capabilities so
// clients can use them the same way they use owned peers.
//
// The sharing model matches upstream Tailscale quarantine semantics:
//   - peers shared *to* the current user are marked jailed, so the shared device
//     can receive incoming traffic but cannot initiate back into the recipient
//     tailnet.
//   - hidden ShareeNode peers on the shared source device are not jailed; they
//     exist only so the source device can receive traffic from recipients.
func (h *Mirage) toNode(
	machine Machine,
	shared bool,
	// baseDomain string,
	// dnsConfig *tailcfg.DNSConfig,
) (*tailcfg.Node, error) {
	var nodeKey key.NodePublic
	err := nodeKey.UnmarshalText([]byte(NodePublicKeyEnsurePrefix(machine.NodeKey)))
	if err != nil {
		log.Trace().
			Str("node_key", machine.NodeKey).
			Msgf("Failed to parse node public key from hex")

		return nil, fmt.Errorf("failed to parse node public key: %w", err)
	}

	var machineKey key.MachinePublic
	// MachineKey is only used in the legacy protocol
	if machine.MachineKey != "" {
		err = machineKey.UnmarshalText(
			[]byte(MachinePublicKeyEnsurePrefix(machine.MachineKey)),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to parse machine public key: %w", err)
		}
	}

	var discoKey key.DiscoPublic
	if machine.DiscoKey != "" {
		err := discoKey.UnmarshalText(
			[]byte(DiscoPublicKeyEnsurePrefix(machine.DiscoKey)),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to parse disco public key: %w", err)
		}
	} else {
		discoKey = key.DiscoPublic{}
	}

	addrs := []netip.Prefix{}
	for _, machineAddress := range machine.IPAddresses {
		ip := netip.PrefixFrom(machineAddress, machineAddress.BitLen())
		addrs = append(addrs, ip)
	}

	allowedIPs := append(
		[]netip.Prefix{},
		addrs...) // we append the node own IP, as it is required by the clients

	primaryRoutes, err := h.getMachinePrimaryRoutes(&machine)
	if err != nil {
		return nil, err
	}
	primaryPrefixes := Routes(primaryRoutes).toPrefixes()

	machineRoutes, err := h.GetMachineRoutes(&machine)
	if err != nil {
		return nil, err
	}
	for _, route := range machineRoutes {
		if route.Enabled && (route.IsPrimary || route.isExitRoute()) {
			allowedIPs = append(allowedIPs, netip.Prefix(route.Prefix))
		}
	}

	if shared {
		allowedIPs = append([]netip.Prefix{}, allowedIPs...)
		primaryPrefixes = append([]netip.Prefix{}, primaryPrefixes...)
	}

	homeDERP := 0
	if machine.HostInfo.NetInfo != nil {
		homeDERP = machine.HostInfo.NetInfo.PreferredDERP
	}
	legacyDERP := fmt.Sprintf("%s:%d", tailcfg.DerpMagicIP, homeDERP)

	var keyExpiry time.Time
	if machine.Expiry != nil {
		keyExpiry = *machine.Expiry
	} else {
		keyExpiry = time.Time{}
	}

	var hostname string
	if machine.User.Organization.EnableMagic { //[cgao6 removed] dnsConfig != nil && dnsConfig.Proxied { // MagicDNS
		_, baseDomain := machine.User.GetDNSConfig(h.cfg.IPPrefixes)
		baseDomain = strings.TrimSuffix(baseDomain, ".")
		hostname = machine.GivenName
		if baseDomain != "" {
			hostname = fmt.Sprintf("%s.%s.", machine.GivenName, baseDomain)
		}
		if len(hostname) > maxHostnameLength {
			return nil, fmt.Errorf(
				"hostname %q is too long it cannot except 255 ASCII chars: %w",
				hostname,
				ErrHostnameTooLong,
			)
		}
	} else {
		hostname = machine.GivenName
	}

	hostInfo := machine.GetHostInfo()
	if machine.ShareeNode {
		hostInfo.ShareeNode = true
	}

	online := machine.isOnline()
	expired := machine.isExpired()

	tags, _ := getTags(machine.User.Organization.AclPolicy, machine, h.cfg.OIDC.StripEmaildomain)
	tags = lo.Uniq(append(tags, machine.ForcedTags...))

	capMap := tailcfg.NodeCapMap{
		tailcfg.CapabilityAdmin: []tailcfg.RawMessage{},
		tailcfg.CapabilitySSH:   []tailcfg.RawMessage{},
	}
	capabilities := []tailcfg.NodeCapability{
		tailcfg.CapabilityAdmin,
		tailcfg.CapabilitySSH,
	}
	if machine.User.Organization.FileSharingEnabledValue() {
		capMap[tailcfg.CapabilityFileSharing] = []tailcfg.RawMessage{}
		capabilities = append(capabilities, tailcfg.CapabilityFileSharing)
	}

	node := tailcfg.Node{
		ID: tailcfg.NodeID(machine.ID), // this is the actual ID
		StableID: tailcfg.StableNodeID(
			strconv.FormatInt(machine.ID, Base10),
		), // in mirage, unlike tailcontrol server, IDs are permanent
		Name: hostname,
		Cap:  tailcfg.CurrentCapabilityVersion,

		User: tailcfg.UserID(machine.UserID),

		Key:       nodeKey,
		KeyExpiry: keyExpiry.UTC(),

		Machine:          machineKey,
		DiscoKey:         discoKey,
		Addresses:        addrs,
		AllowedIPs:       allowedIPs,
		Endpoints:        parseMachineEndpoints(machine.Endpoints),
		HomeDERP:         homeDERP,
		LegacyDERPString: legacyDERP,
		Hostinfo:         hostInfo.View(),
		Created:          machine.CreatedAt.UTC(),
		IsJailed:         shared,

		Tags: tags,

		PrimaryRoutes: primaryPrefixes,

		LastSeen:          machine.LastSeen,
		Online:            &online,
		MachineAuthorized: !expired,
		Expired:           expired,

		Capabilities: capabilities,
		CapMap:       capMap,
	}

	return &node, nil
}

func parseMachineEndpoints(endpoints []string) []netip.AddrPort {
	parsed := make([]netip.AddrPort, 0, len(endpoints))
	for _, endpoint := range endpoints {
		addrPort, err := netip.ParseAddrPort(endpoint)
		if err != nil {
			log.Warn().
				Err(err).
				Str("endpoint", endpoint).
				Msg("ignoring invalid machine endpoint")
			continue
		}
		parsed = append(parsed, addrPort)
	}

	return parsed
}

// getTags will return the tags of the current machine.
// Invalid tags are tags added by a user on a node, and that user doesn't have authority to add this tag.
// Valid tags are tags added by a user that is allowed in the ACL policy to add this tag.
func getTags(
	aclPolicy *ACLPolicy,
	machine Machine,
	stripEmailDomain bool,
) ([]string, []string) {
	validTags := make([]string, 0)
	invalidTags := make([]string, 0)
	if aclPolicy == nil {
		return validTags, invalidTags
	}
	validTagMap := make(map[string]bool)
	invalidTagMap := make(map[string]bool)
	for _, tag := range machine.HostInfo.RequestTags {
		owners, err := expandTagOwners(*aclPolicy, tag, stripEmailDomain)
		if errors.Is(err, errInvalidTag) {
			invalidTagMap[tag] = true

			continue
		}
		var found bool
		for _, owner := range owners {
			if machine.User.Name == owner {
				found = true
			}
		}
		if found {
			validTagMap[tag] = true
		} else {
			invalidTagMap[tag] = true
		}
	}
	for tag := range invalidTagMap {
		invalidTags = append(invalidTags, tag)
	}
	for tag := range validTagMap {
		validTags = append(validTags, tag)
	}

	return validTags, invalidTags
}

// RegisterMachine is executed from the CLI to register a new Machine using its MachineKey.
func (h *Mirage) RegisterMachine(machine Machine,
) (*Machine, error) {
	log.Debug().
		Str("machine", machine.Hostname).
		Str("machine_key", machine.MachineKey).
		Str("node_key", machine.NodeKey).
		Str("user", machine.User.Name).
		Msg("Registering machine")

	// If the machine exists and we had already IPs for it, we just save it
	// so we store the machine.Expire and machine.Nodekey that has been set when
	// adding it to the registrationCache
	if len(machine.IPAddresses) > 0 {
		if err := h.db.Save(&machine).Error; err != nil {
			return nil, fmt.Errorf("failed register existing machine in the database: %w", err)
		}

		log.Trace().
			Str("machine", machine.Hostname).
			Str("machine_key", machine.MachineKey).
			Str("node_key", machine.NodeKey).
			Str("user", machine.User.Name).
			Msg("Machine authorized again")

		return &machine, nil
	}

	h.ipAllocationMutex.Lock()
	defer h.ipAllocationMutex.Unlock()

	ips, err := h.getAvailableIPs()
	if err != nil {
		log.Error().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not find IP for the new machine")

		return nil, err
	}

	machine.IPAddresses = ips

	if err := h.db.Save(&machine).Error; err != nil {
		return nil, fmt.Errorf("failed register(save) machine in the database: %w", err)
	}

	log.Trace().
		Str("machine", machine.Hostname).
		Str("ip", strings.Join(ips.ToStringSlice(), ",")).
		Msg("Machine registered with the database")

	return &machine, nil
}

// GetAdvertisedRoutes returns the routes that are be advertised by the given machine.
func (h *Mirage) GetAdvertisedRoutes(machine *Machine) ([]netip.Prefix, error) {
	routes := []Route{}

	err := h.db.
		Preload("Machine").
		Where("machine_id = ? AND advertised = ?", machine.ID, true).Find(&routes).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not get advertised routes for machine")

		return nil, err
	}

	prefixes := []netip.Prefix{}
	for _, route := range routes {
		prefixes = append(prefixes, netip.Prefix(route.Prefix))
	}

	return prefixes, nil
}

// GetEnabledRoutes returns the routes that are enabled for the machine.
func (h *Mirage) GetEnabledRoutes(machine *Machine) ([]netip.Prefix, error) {
	routes := []Route{}

	err := h.db.
		Preload("Machine").
		Where("machine_id = ? AND advertised = ? AND enabled = ?", machine.ID, true, true).
		Find(&routes).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not get enabled routes for machine")

		return nil, err
	}

	prefixes := []netip.Prefix{}
	for _, route := range routes {
		prefixes = append(prefixes, netip.Prefix(route.Prefix))
	}

	return prefixes, nil
}

func (h *Mirage) IsRoutesEnabled(machine *Machine, routeStr string) bool {
	route, err := netip.ParsePrefix(routeStr)
	if err != nil {
		return false
	}

	enabledRoutes, err := h.GetEnabledRoutes(machine)
	if err != nil {
		log.Error().Err(err).Msg("Could not get enabled routes")

		return false
	}

	for _, enabledRoute := range enabledRoutes {
		if route == enabledRoute {
			return true
		}
	}

	return false
}

// enableRoutes enables new routes based on a list of new routes.
func (h *Mirage) enableRoutes(machine *Machine, routeStrs ...string) error {
	newRoutes := make([]netip.Prefix, len(routeStrs))
	for index, routeStr := range routeStrs {
		route, err := netip.ParsePrefix(routeStr)
		if err != nil {
			return err
		}

		newRoutes[index] = route
	}

	advertisedRoutes, err := h.GetAdvertisedRoutes(machine)
	if err != nil {
		return err
	}

	for _, newRoute := range newRoutes {
		if !contains(advertisedRoutes, newRoute) {
			return fmt.Errorf(
				"route (%s) is not available on node %s: %w",
				machine.Hostname,
				newRoute, ErrMachineRouteIsNotAvailable,
			)
		}
	}

	// Separate loop so we don't leave things in a half-updated state
	for _, prefix := range newRoutes {
		route := Route{}
		err := h.db.Preload("Machine").
			Where("machine_id = ? AND prefix = ?", machine.ID, IPPrefix(prefix)).
			First(&route).Error
		if err == nil {
			route.Enabled = true

			// Mark already as primary if there is only this node offering this subnet
			// (and is not an exit route)
			if !route.isExitRoute() {
				route.IsPrimary = h.isUniquePrefix(route)
			}

			err = h.db.Save(&route).Error
			if err != nil {
				return fmt.Errorf("failed to enable route: %w", err)
			}
		} else {
			return fmt.Errorf("failed to find route: %w", err)
		}
	}

	h.setOrgLastStateChangeToNow(machine.User.OrganizationID)

	return nil
}

// EnableAutoApprovedRoutes enables any routes advertised by a machine that match the ACL autoApprovers policy.
func (h *Mirage) EnableAutoApprovedRoutes(machine *Machine) error {
	if len(machine.IPAddresses) == 0 {
		return nil // This machine has no IPAddresses, so can't possibly match any autoApprovers ACLs
	}

	routes := []Route{}
	err := h.db.
		Preload("Machine").
		Where("machine_id = ? AND advertised = true AND enabled = false", machine.ID).
		Find(&routes).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Error().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Msg("Could not get advertised routes for machine")

		return err
	}

	approvedRoutes := []Route{}

	for _, advertisedRoute := range routes {
		if machine.User.Organization.AclPolicy == nil {
			continue
		}
		routeApprovers, err := machine.User.Organization.AclPolicy.AutoApprovers.GetRouteApprovers(
			netip.Prefix(advertisedRoute.Prefix),
		)
		if err != nil {
			log.Err(err).
				Str("advertisedRoute", advertisedRoute.String()).
				Int64("machineId", machine.ID).
				Msg("Failed to resolve autoApprovers for advertised route")

			return err
		}

		for _, approvedAlias := range routeApprovers {
			if approvedAlias == machine.User.Name {
				approvedRoutes = append(approvedRoutes, advertisedRoute)
			} else {
				approvedIps, err := h.expandAlias(false, []Machine{*machine}, machine.UserID, *(machine.User.Organization.AclPolicy), approvedAlias, h.cfg.OIDC.StripEmaildomain)
				if err != nil {
					log.Err(err).
						Str("alias", approvedAlias).
						Msg("Failed to expand alias when processing autoApprovers policy")

					return err
				}

				// approvedIPs should contain all of machine's IPs if it matches the rule, so check for first
				if contains(approvedIps, machine.IPAddresses[0].String()) {
					approvedRoutes = append(approvedRoutes, advertisedRoute)
				}
			}
		}
	}

	for i, approvedRoute := range approvedRoutes {
		approvedRoutes[i].Enabled = true
		err = h.db.Save(&approvedRoutes[i]).Error
		if err != nil {
			log.Err(err).
				Str("approvedRoute", approvedRoute.String()).
				Int64("machineId", machine.ID).
				Msg("Failed to enable approved route")

			return err
		}
	}

	return nil
}

func machinesByID(machines Machines) map[int64]Machine {
	byID := make(map[int64]Machine)
	for _, machine := range machines {
		byID[machine.ID] = machine
	}

	return byID
}
