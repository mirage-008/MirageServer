package controller

import (
	"encoding/binary"
	"encoding/json"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/util/zstdframe"
)

// mapResponseStreamState tracks state associated with a stream of MapResponse messages,
// which may optionally send only deltas from the previous message.
type mapResponseStreamState struct {
	// peerNodesByID is the peer node state sent in the last stream message,
	// for comparison in generating deltas in the new message.
	peerNodesByID map[tailcfg.NodeID]*tailcfg.Node
}

func allowedFilterDestinations(machine *Machine) []netip.Prefix {
	if machine == nil {
		return nil
	}

	allowedDestinations := make([]netip.Prefix, 0, len(machine.IPAddresses)+len(machine.HostInfo.RoutableIPs)+4)
	for _, addr := range machine.IPAddresses {
		allowedDestinations = append(allowedDestinations, netip.PrefixFrom(addr, addr.BitLen()))
	}
	for _, prefix := range machine.GetHostInfo().RoutableIPs {
		allowedDestinations = append(allowedDestinations, prefix)
	}
	for _, prefix := range allowedDestinations {
		if prefix.Bits() == 0 {
			allowedDestinations = append(allowedDestinations, netip.PrefixFrom(netip.IPv4Unspecified(), 32))
			allowedDestinations = append(allowedDestinations, netip.PrefixFrom(netip.MustParseAddr("2000::"), 128))
			break
		}
	}

	return allowedDestinations
}

func reduceFilterRulesForMachine(machine *Machine, rules []tailcfg.FilterRule) []tailcfg.FilterRule {
	if machine == nil || len(rules) == 0 {
		return []tailcfg.FilterRule{}
	}

	allowedDestinations := allowedFilterDestinations(machine)
	reduced := make([]tailcfg.FilterRule, 0, len(rules))
	for _, rule := range rules {
		if len(rule.DstPorts) == 0 {
			reduced = append(reduced, tailcfg.FilterRule{
				SrcIPs:   append([]string{}, rule.SrcIPs...),
				SrcBits:  append([]int{}, rule.SrcBits...),
				IPProto:  append([]int{}, rule.IPProto...),
				CapGrant: append([]tailcfg.CapGrant{}, rule.CapGrant...),
			})
			continue
		}

		dests := make([]tailcfg.NetPortRange, 0, len(rule.DstPorts))
		for _, dest := range rule.DstPorts {
			if dest.IP == "*" {
				dests = append(dests, dest)
				continue
			}
			prefix, err := netip.ParsePrefix(dest.IP)
			if err != nil {
				if addr, addrErr := netip.ParseAddr(dest.IP); addrErr == nil {
					prefix = netip.PrefixFrom(addr, addr.BitLen())
				} else {
					continue
				}
			}
			for _, allowed := range allowedDestinations {
				if prefix.Overlaps(allowed) || allowed.Overlaps(prefix) {
					dests = append(dests, dest)
					break
				}
			}
		}
		if len(dests) == 0 {
			continue
		}
		reduced = append(reduced, tailcfg.FilterRule{
			SrcIPs:   append([]string{}, rule.SrcIPs...),
			SrcBits:  append([]int{}, rule.SrcBits...),
			DstPorts: dests,
			IPProto:  append([]int{}, rule.IPProto...),
			CapGrant: append([]tailcfg.CapGrant{}, rule.CapGrant...),
		})
	}

	return reduced
}

func packetFiltersForMachine(machine *Machine, rules []tailcfg.FilterRule) ([]tailcfg.FilterRule, map[string][]tailcfg.FilterRule) {
	reduced := reduceFilterRulesForMachine(machine, rules)
	if len(reduced) == 0 {
		return nil, map[string][]tailcfg.FilterRule{"base": {}}
	}

	return reduced, map[string][]tailcfg.FilterRule{"base": reduced}
}

func peerNodesForMachine(h *Mirage, peers Machines) ([]*tailcfg.Node, error) {
	return h.toNodes(peers)
}

func nodesByID(nodes []*tailcfg.Node) map[tailcfg.NodeID]*tailcfg.Node {
	byID := make(map[tailcfg.NodeID]*tailcfg.Node, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		byID[node.ID] = node
	}

	return byID
}

func cloneNodesByID(nodes []*tailcfg.Node) map[tailcfg.NodeID]*tailcfg.Node {
	byID := make(map[tailcfg.NodeID]*tailcfg.Node, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		byID[node.ID] = node.Clone()
	}

	return byID
}

func (h *Mirage) generateMapResponse(
	mapRequest tailcfg.MapRequest,
	machine *Machine,
	streamState *mapResponseStreamState,
) (*tailcfg.MapResponse, error) {
	log.Trace().
		Str("func", "generateMapResponse").
		Str("machine", mapRequest.Hostinfo.Hostname).
		Msg("Creating Map response")

	//cgao6: change to use User's DNSConfig
	node, err := h.toNode(*machine, machine.Shared) //h.cfg.BaseDomain, h.cfg.DNSConfig)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Cannot convert to node")

		return nil, err
	}

	org, err := h.GetOrgnaizationByID(machine.User.OrganizationID)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Cannot get organization of the machine")

		return nil, err
	}
	// enableSelf 表示该节点的用户是否启用了self
	enableSelf, err := h.UpdateACLRulesOfOrg(org, &machine.User, machine)

	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Cannot get ACL rules")

		return nil, err
	}
	// organization be set to field :machine.User.Organization
	machine.User.Organization = *org
	peers, invalidNodeIDs, err := h.getValidPeers(machine, enableSelf)
	if invalidNodeIDs != nil {
		log.Trace().Msg("Should ignore invalidNodeIDs for current")
	}
	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Cannot fetch peers")

		return nil, err
	}

	profiles := h.getMapResponseUserProfiles(*machine, peers)

	//cgao6: use User's DNSconfig instead
	dnsConfig := getMapResponseDNSConfig(
		h.cfg.IPPrefixes, //
		//		h.cfg.DNSConfig,
		//		h.cfg.BaseDomain,
		*machine,
		peers,
	)

	now := time.Now()
	//org := &machine.User.Organization

	derpMap, err := h.LoadOrgDERPs(machine.User.OrganizationID)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Failed to get DERP map of machine")
	}

	reducedRules, packetFilters := packetFiltersForMachine(machine, org.AclRules)
	flowLogCfg := normalizeFlowLogConfig(h.cfg.FlowLogCfg)

	resp := tailcfg.MapResponse{
		KeepAlive: false,
		Node:      node,

		// TODO: Only send if updated
		DERPMap: derpMap, //cgao6: h.DERPMap,

		// TODO(kradalby): Implement:
		// https://github.com/tailscale/tailscale/blob/main/tailcfg/tailcfg.go#L1351-L1374
		// PeersChanged
		// PeersRemoved
		// PeersChangedPatch
		// PeerSeenChange
		// OnlineChange

		// TODO: Only send if updated
		DNSConfig: dnsConfig,

		// TODO: Only send if updated
		Domain: org.Name,

		// Do not instruct clients to collect services, we do not
		// support or do anything with them
		CollectServices: "false",

		PacketFilter:  reducedRules,
		PacketFilters: packetFilters,

		UserProfiles: profiles,

		// TODO: Only send if updated
		SSHPolicy: org.SshPolicy,

		ControlTime: &now,

		Debug: &tailcfg.Debug{
			DisableLogTail: !flowLogCfg.Enabled,
		},
	}
	if flowLogCfg.Enabled {
		domainAuditLogID, err := h.ensureOrganizationDomainAuditLogID(org)
		if err != nil {
			log.Error().Caller().Err(err).Msg("failed to ensure organization domain audit log id")
		} else {
			nodeAuditLogID, err := h.ensureMachineDataPlaneAuditLogID(machine)
			if err != nil {
				log.Error().Caller().Err(err).Msg("failed to ensure machine data plane audit log id")
			} else {
				resp.DomainDataPlaneAuditLogID = domainAuditLogID
				resp.Node.DataPlaneAuditLogID = nodeAuditLogID
				if resp.Node.CapMap == nil {
					resp.Node.CapMap = tailcfg.NodeCapMap{}
				}
				resp.Node.CapMap[tailcfg.CapabilityDataPlaneAuditLogs] = []tailcfg.RawMessage{}
				resp.Node.Capabilities = appendNodeCapabilityIfMissing(resp.Node.Capabilities, tailcfg.CapabilityDataPlaneAuditLogs)
				if flowLogCfg.LogExitFlows {
					resp.Node.CapMap[tailcfg.NodeAttrLogExitFlows] = []tailcfg.RawMessage{}
					resp.Node.Capabilities = appendNodeCapabilityIfMissing(resp.Node.Capabilities, tailcfg.NodeAttrLogExitFlows)
				}
			}
		}
	}

	toNodes := func(machines Machines) ([]*tailcfg.Node, error) {
		return peerNodesForMachine(h, machines)
	}
	resp, err = applyMapResponseDelta(resp, streamState, peers, toNodes)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "generateMapResponse").
			Err(err).
			Msg("Cannot apply map response deltas")

		return nil, err
	}
	resp.ClientVersion = &tailcfg.ClientVersion{}

	if mapRequest.Hostinfo.OS == "windows" {
		if IsUpdateAvailable(mapRequest.Hostinfo.IPNVersion, h.cfg.ClientVersion.Win.Version) {
			resp.ClientVersion.RunningLatest = false
			resp.ClientVersion.LatestVersion = strings.Split(h.cfg.ClientVersion.Win.Version, "-")[0]
			resp.ClientVersion.NotifyURL = h.cfg.ClientVersion.Win.Url
		} else {
			resp.ClientVersion.RunningLatest = true
		}
	}

	log.Trace().
		Str("func", "generateMapResponse").
		Str("machine", mapRequest.Hostinfo.Hostname).
		// Interface("payload", resp).
		Msgf("Generated map response: %s", tailMapResponseToString(resp))

	return &resp, nil
}

func (h *Mirage) getMapResponseData(
	mapRequest tailcfg.MapRequest,
	machine *Machine,
	streamState *mapResponseStreamState,
) ([]byte, error) {
	mapResponse, err := h.generateMapResponse(mapRequest, machine, streamState)
	if err != nil {
		return nil, err
	}

	return h.marshalMapResponse(mapResponse, key.MachinePublic{}, mapRequest.Compress)

}

func (h *Mirage) getMapKeepAliveResponseData(
	mapRequest tailcfg.MapRequest,
	machine *Machine,
) ([]byte, error) {
	keepAliveResponse := tailcfg.MapResponse{
		KeepAlive: true,
	}

	return h.marshalMapResponse(keepAliveResponse, key.MachinePublic{}, mapRequest.Compress)

}

func (h *Mirage) marshalResponse(
	resp interface{},
	machineKey key.MachinePublic,
) ([]byte, error) {
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		log.Error().
			Caller().
			Err(err).
			Msg("Cannot marshal response")

		return nil, err
	}

	return jsonBody, nil

}

func (h *Mirage) marshalMapResponse(
	resp interface{},
	machineKey key.MachinePublic,
	compression string,
) ([]byte, error) {
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		log.Error().
			Caller().
			Err(err).
			Msg("Cannot marshal map response")
	}

	var respBody []byte
	if compression == ZstdCompression {
		respBody = zstdEncode(jsonBody)
	} else {
		respBody = jsonBody
	}

	data := make([]byte, reservedResponseHeaderSize)
	binary.LittleEndian.PutUint32(data, uint32(len(respBody)))
	data = append(data, respBody...)

	return data, nil
}

func zstdEncode(in []byte) []byte {
	return zstdframe.AppendEncode(nil, in, zstdframe.FastestCompression)
}

// applyMapResponseDelta returns a modified MapResponse
// with fields modified which make use of delta (send on changes).
//
// mapResponse the current mapResponse with delta fields not set.
// streamState optional previous state of mapResponse sent in this stream. Set to nil for a "full update" (no deltas).
// currentPeers list of peers currently available for the node that this mapResponse is for.
// toNodes a function to convert the Headscale Machines structure to Tailscale Nodes structure.
func applyMapResponseDelta(
	mapResponse tailcfg.MapResponse,
	streamState *mapResponseStreamState,
	currentPeers Machines,
	toNodes func(Machines) ([]*tailcfg.Node, error)) (tailcfg.MapResponse, error) {
	nodePeers, err := toNodes(currentPeers)
	if err != nil {
		return tailcfg.MapResponse{}, err
	}

	if streamState == nil {
		mapResponse.Peers = nodePeers
		return mapResponse, nil
	}

	currentPeerNodesByID := nodesByID(nodePeers)

	if streamState.peerNodesByID == nil {
		// 1st map, send full nodes
		mapResponse.Peers = nodePeers
	} else {
		// Update PeersChanged with any peers which were added or changed.
		nodesChanged := make([]*tailcfg.Node, 0, len(currentPeerNodesByID))
		for id, peerNode := range currentPeerNodesByID {
			previousPeerNode, hadPrevious := streamState.peerNodesByID[id]
			if !hadPrevious || !previousPeerNode.Equal(peerNode) {
				nodesChanged = append(nodesChanged, peerNode)
			}
		}
		sort.Slice(nodesChanged, func(i, j int) bool {
			return nodesChanged[i].ID < nodesChanged[j].ID
		})
		mapResponse.PeersChanged = nodesChanged

		// Update PeersRemoved with any peers which are no longer present.
		peersRemoved := make([]tailcfg.NodeID, 0)
		for id := range streamState.peerNodesByID {
			if _, has := currentPeerNodesByID[id]; !has {
				peersRemoved = append(peersRemoved, id)
			}
		}
		sort.Slice(peersRemoved, func(i, j int) bool {
			return peersRemoved[i] < peersRemoved[j]
		})
		mapResponse.PeersRemoved = peersRemoved
	}

	// Update streamState for use in the next message.
	streamState.peerNodesByID = cloneNodesByID(nodePeers)

	// TODO(kallen): Also Implement the following deltas for even smaller
	// message sizes:
	//
	// PeersChangedPatch
	// PeerSeenChange
	// OnlineChange

	return mapResponse, nil
}
