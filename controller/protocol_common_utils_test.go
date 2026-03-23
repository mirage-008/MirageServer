package controller

import (
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

func TestApplyMapResponseDeltaInitialMapSendsFullPeers(t *testing.T) {
	t.Parallel()

	nodes := map[int64]*tailcfg.Node{
		1: deltaTestNode(1, []string{"100.64.0.1/32"}, nil, nil),
		2: deltaTestNode(2, []string{"100.64.0.2/32"}, nil, nil),
	}
	machines := Machines{{ID: 1}, {ID: 2}}
	streamState := &mapResponseStreamState{}

	resp, err := applyMapResponseDelta(tailcfg.MapResponse{}, streamState, machines, deltaTestToNodes(nodes))
	if err != nil {
		t.Fatalf("applyMapResponseDelta returned error: %v", err)
	}
	if len(resp.Peers) != 2 {
		t.Fatalf("expected full peer list on initial response, got %d peers", len(resp.Peers))
	}
	if !resp.Peers[0].Equal(nodes[1]) || !resp.Peers[1].Equal(nodes[2]) {
		t.Fatalf("unexpected full peer payload: got %+v", resp.Peers)
	}
	if len(resp.PeersChanged) != 0 {
		t.Fatalf("expected no peer deltas on initial response, got %+v", resp.PeersChanged)
	}
	if len(resp.PeersRemoved) != 0 {
		t.Fatalf("expected no removed peers on initial response, got %+v", resp.PeersRemoved)
	}
	if streamState.peerNodesByID[1] == nodes[1] || streamState.peerNodesByID[2] == nodes[2] {
		t.Fatal("expected stream state to store cloned peer snapshots")
	}
}

func TestApplyMapResponseDeltaDetectsNodeContentChanges(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*tailcfg.Node)
	}{
		{
			name: "allowed IPs",
			mutate: func(node *tailcfg.Node) {
				node.AllowedIPs = append(node.AllowedIPs, netip.MustParsePrefix("10.10.0.0/24"))
			},
		},
		{
			name: "primary routes",
			mutate: func(node *tailcfg.Node) {
				node.PrimaryRoutes = append(node.PrimaryRoutes, netip.MustParsePrefix("10.10.0.0/24"))
			},
		},
		{
			name: "capabilities",
			mutate: func(node *tailcfg.Node) {
				node.Capabilities = append(node.Capabilities, tailcfg.CapabilityFileSharing)
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lastUpdate := time.Unix(1700000000, 0).UTC()
			machines := Machines{{ID: 1, LastSuccessfulUpdate: &lastUpdate}}
			node := deltaTestNode(1, []string{"100.64.0.1/32"}, nil, []tailcfg.NodeCapability{tailcfg.CapabilityAdmin})
			nodes := map[int64]*tailcfg.Node{1: node}
			streamState := &mapResponseStreamState{}

			if _, err := applyMapResponseDelta(tailcfg.MapResponse{}, streamState, machines, deltaTestToNodes(nodes)); err != nil {
				t.Fatalf("initial applyMapResponseDelta returned error: %v", err)
			}
			if streamState.peerNodesByID[1] == node {
				t.Fatal("expected initial stream snapshot to be cloned")
			}

			tc.mutate(node)

			resp, err := applyMapResponseDelta(tailcfg.MapResponse{}, streamState, machines, deltaTestToNodes(nodes))
			if err != nil {
				t.Fatalf("second applyMapResponseDelta returned error: %v", err)
			}
			if len(resp.PeersChanged) != 1 {
				t.Fatalf("expected one changed peer after %s mutation, got %+v", tc.name, resp.PeersChanged)
			}
			if !resp.PeersChanged[0].Equal(node) {
				t.Fatalf("unexpected changed peer payload after %s mutation: got %+v want %+v", tc.name, resp.PeersChanged[0], node)
			}
			if len(resp.PeersRemoved) != 0 {
				t.Fatalf("expected no removed peers after %s mutation, got %+v", tc.name, resp.PeersRemoved)
			}
		})
	}
}

func TestApplyMapResponseDeltaDetectsPeerAdditionAndRemoval(t *testing.T) {
	t.Parallel()

	nodes := map[int64]*tailcfg.Node{
		1: deltaTestNode(1, []string{"100.64.0.1/32"}, nil, nil),
	}
	streamState := &mapResponseStreamState{}

	if _, err := applyMapResponseDelta(
		tailcfg.MapResponse{},
		streamState,
		Machines{{ID: 1}},
		deltaTestToNodes(nodes),
	); err != nil {
		t.Fatalf("initial applyMapResponseDelta returned error: %v", err)
	}

	nodes[2] = deltaTestNode(2, []string{"100.64.0.2/32"}, nil, nil)
	resp, err := applyMapResponseDelta(
		tailcfg.MapResponse{},
		streamState,
		Machines{{ID: 1}, {ID: 2}},
		deltaTestToNodes(nodes),
	)
	if err != nil {
		t.Fatalf("addition applyMapResponseDelta returned error: %v", err)
	}
	if len(resp.PeersChanged) != 1 || resp.PeersChanged[0].ID != tailcfg.NodeID(2) {
		t.Fatalf("expected only new peer in PeersChanged, got %+v", resp.PeersChanged)
	}
	if len(resp.PeersRemoved) != 0 {
		t.Fatalf("expected no removed peers after addition, got %+v", resp.PeersRemoved)
	}

	delete(nodes, 1)
	resp, err = applyMapResponseDelta(
		tailcfg.MapResponse{},
		streamState,
		Machines{{ID: 2}},
		deltaTestToNodes(nodes),
	)
	if err != nil {
		t.Fatalf("removal applyMapResponseDelta returned error: %v", err)
	}
	if len(resp.PeersChanged) != 0 {
		t.Fatalf("expected no changed peers on pure removal, got %+v", resp.PeersChanged)
	}
	if !reflect.DeepEqual(resp.PeersRemoved, []tailcfg.NodeID{tailcfg.NodeID(1)}) {
		t.Fatalf("unexpected removed peers: got %+v", resp.PeersRemoved)
	}
}

func deltaTestToNodes(nodes map[int64]*tailcfg.Node) func(Machines) ([]*tailcfg.Node, error) {
	return func(machines Machines) ([]*tailcfg.Node, error) {
		result := make([]*tailcfg.Node, 0, len(machines))
		for _, machine := range machines {
			node, ok := nodes[machine.ID]
			if !ok {
				return nil, fmt.Errorf("missing test node for machine %d", machine.ID)
			}
			result = append(result, node)
		}

		return result, nil
	}
}

func deltaTestNode(id int64, allowedIPs []string, primaryRoutes []string, capabilities []tailcfg.NodeCapability) *tailcfg.Node {
	node := &tailcfg.Node{
		ID:            tailcfg.NodeID(id),
		StableID:      tailcfg.StableNodeID(fmt.Sprintf("%d", id)),
		AllowedIPs:    make([]netip.Prefix, 0, len(allowedIPs)),
		Capabilities:  append([]tailcfg.NodeCapability{}, capabilities...),
		PrimaryRoutes: make([]netip.Prefix, 0, len(primaryRoutes)),
	}
	for _, prefix := range allowedIPs {
		node.AllowedIPs = append(node.AllowedIPs, netip.MustParsePrefix(prefix))
	}
	for _, prefix := range primaryRoutes {
		node.PrimaryRoutes = append(node.PrimaryRoutes, netip.MustParsePrefix(prefix))
	}

	return node
}
