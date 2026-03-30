package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tailscale.com/net/netcheck"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const (
	smokeSubnetRoute       = "10.123.45.0/24"
	sharedSmokeSubnetRoute = "192.168.1.0/24"

	rapidReconnectObservationWindow = 8 * time.Second
)

type smokeServer struct {
	app        *Mirage
	serverAddr string
}

type smokeNode struct {
	hostname      string
	socketPath    string
	stateDir      string
	httpProxyAddr string
	tailscaledLog *strings.Builder
}

type smokeStatus struct {
	BackendState   string                     `json:"BackendState"`
	TailscaleIPs   []string                   `json:"TailscaleIPs"`
	Self           *smokePeerStatus           `json:"Self"`
	Peer           map[string]smokePeerStatus `json:"Peer"`
	ExitNodeStatus *smokeExitNodeStatus       `json:"ExitNodeStatus,omitempty"`
	Health         []string                   `json:"Health,omitempty"`
}

type smokeExitNodeStatus struct {
	ID           string   `json:"ID"`
	Online       bool     `json:"Online"`
	TailscaleIPs []string `json:"TailscaleIPs"`
}

type smokePeerStatus struct {
	ID             string   `json:"ID"`
	HostName       string   `json:"HostName"`
	DNSName        string   `json:"DNSName"`
	TailscaleIPs   []string `json:"TailscaleIPs"`
	AllowedIPs     []string `json:"AllowedIPs"`
	PrimaryRoutes  []string `json:"PrimaryRoutes"`
	Addrs          []string `json:"Addrs"`
	CurAddr        string   `json:"CurAddr"`
	Relay          string   `json:"Relay"`
	PeerRelay      string   `json:"PeerRelay"`
	Online         bool     `json:"Online"`
	InNetworkMap   bool     `json:"InNetworkMap"`
	ShareeNode     bool     `json:"ShareeNode"`
	ExitNode       bool     `json:"ExitNode"`
	ExitNodeOption bool     `json:"ExitNodeOption"`
}

func TestOfficialClientSubnetRouteAutoApproveSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	if org.AclPolicy.AutoApprovers.Routes == nil {
		org.AclPolicy.AutoApprovers.Routes = make(map[string][]string)
	}
	org.AclPolicy.AutoApprovers.Routes[smokeSubnetRoute] = []string{user.Name}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	authKeyA, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeA): %v", err)
	}
	authKeyB, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeB): %v", err)
	}

	nodeA := startSmokeNode(
		t,
		filepath.Join(tempDir, "route-advertiser"),
		server.serverAddr,
		authKeyA.Key,
		"route-advertiser",
		"--advertise-routes="+smokeSubnetRoute,
	)
	nodeB := startSmokeNode(
		t,
		filepath.Join(tempDir, "route-consumer"),
		server.serverAddr,
		authKeyB.Key,
		"route-consumer",
		"--accept-routes=true",
	)

	_ = waitForNodeRunning(t, nodeA.socketPath)
	_ = waitForNodeRunning(t, nodeB.socketPath)

	advertiser := waitForMachineWithRouteState(t, server.app, nodeA.hostname, smokeSubnetRoute, true)
	advertiser = waitForMachinePrimaryRoute(t, server.app, nodeA.hostname, smokeSubnetRoute)
	consumer := waitForMachine(t, server.app, nodeB.hostname)

	enabledRoutes, err := server.app.GetEnabledRoutes(advertiser)
	if err != nil {
		t.Fatalf("GetEnabledRoutes(%s): %v", nodeA.hostname, err)
	}
	targetRoute := smokeMustPrefix(t, smokeSubnetRoute)
	if !containsPrefix(enabledRoutes, targetRoute) {
		t.Fatalf("expected enabled routes for %s to include %s, got %v", nodeA.hostname, smokeSubnetRoute, enabledRoutes)
	}

	advertiserNode, err := server.app.toNode(*advertiser, advertiser.Shared)
	if err != nil {
		t.Fatalf("toNode(%s): %v", nodeA.hostname, err)
	}
	if !containsPrefix(advertiserNode.AllowedIPs, targetRoute) {
		t.Fatalf("expected advertiser allowed IPs to include %s, got %v", smokeSubnetRoute, advertiserNode.AllowedIPs)
	}
	if !containsPrefix(advertiserNode.PrimaryRoutes, targetRoute) {
		t.Fatalf("expected advertiser primary routes to include %s, got %v", smokeSubnetRoute, advertiserNode.PrimaryRoutes)
	}

	org, err = server.app.GetOrgnaizationByID(advertiser.User.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(advertiser): %v", err)
	}
	enableSelf, err := server.app.UpdateACLRulesOfOrg(org, &consumer.User, consumer)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	advertiser.User.Organization = *org
	consumer.User.Organization = *org
	peers, _, err := server.app.getValidPeers(consumer, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(%s): %v", nodeB.hostname, err)
	}
	if len(peers) == 0 {
		t.Fatalf("expected peers for %s after ACL update", nodeB.hostname)
	}
	waitForPeerVisible(t, nodeB.socketPath, nodeA.hostname)
	peerNodes, err := server.app.toNodes(peers)
	if err != nil {
		t.Fatalf("toNodes(peers): %v", err)
	}
	var advertiserPeerNode *tailcfg.Node
	for _, peerNode := range peerNodes {
		if peerNode != nil && peerNode.Name == advertiserNode.Name {
			advertiserPeerNode = peerNode
			break
		}
	}
	if advertiserPeerNode == nil {
		t.Fatalf("expected advertiser node %s in consumer peer map, got %d peers", advertiserNode.Name, len(peerNodes))
	}
	if !containsPrefix(advertiserPeerNode.AllowedIPs, targetRoute) {
		t.Fatalf("expected peer allowed IPs to include %s, got %v", smokeSubnetRoute, advertiserPeerNode.AllowedIPs)
	}
	if !containsPrefix(advertiserPeerNode.PrimaryRoutes, targetRoute) {
		t.Fatalf("expected peer primary routes to include %s, got %v", smokeSubnetRoute, advertiserPeerNode.PrimaryRoutes)
	}

	_ = waitForPeerVisible(t, nodeB.socketPath, nodeA.hostname)
}

func TestOfficialClientSubnetRouteReachabilitySmoke(t *testing.T) {
	requireSmokeEnv(t)

	targetIP := smokeLocalPrivateIPv4(t)
	targetRoute := netip.PrefixFrom(targetIP, targetIP.BitLen()).String()

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	org.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{user.Name},
		Destinations: []string{targetIP.String() + ":*"},
	}}
	if org.AclPolicy.AutoApprovers.Routes == nil {
		org.AclPolicy.AutoApprovers.Routes = make(map[string][]string)
	}
	org.AclPolicy.AutoApprovers.Routes[targetRoute] = []string{user.Name}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	routerAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(router): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	routerNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "reachability-router"),
		server.serverAddr,
		routerAuthKey.Key,
		"reachability-router",
		"--advertise-routes="+targetRoute,
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "reachability-client"),
		server.serverAddr,
		clientAuthKey.Key,
		"reachability-client",
	)

	_ = waitForNodeRunning(t, routerNode.socketPath)
	_ = waitForNodeRunning(t, clientNode.socketPath)

	routerMachine := waitForMachineWithRouteState(t, server.app, routerNode.hostname, targetRoute, true)
	routerMachine = waitForMachinePrimaryRoute(t, server.app, routerNode.hostname, targetRoute)
	clientMachine := waitForMachine(t, server.app, clientNode.hostname)

	org, err = server.app.GetOrgnaizationByID(routerMachine.User.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(router): %v", err)
	}
	enableSelf, err := server.app.UpdateACLRulesOfOrg(org, &clientMachine.User, clientMachine)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	routerMachine.User.Organization = *org
	clientMachine.User.Organization = *org
	peers, _, err := server.app.getValidPeers(clientMachine, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(%s): %v", clientNode.hostname, err)
	}
	if len(peers) == 0 {
		t.Fatalf("expected peers for %s after ACL update", clientNode.hostname)
	}

	peerNodes, err := server.app.toNodes(peers)
	if err != nil {
		t.Fatalf("toNodes(peers): %v", err)
	}
	var routerPeerNode *tailcfg.Node
	for _, peerNode := range peerNodes {
		if peerNode != nil && peerNode.ID == tailcfg.NodeID(routerMachine.ID) {
			routerPeerNode = peerNode
			break
		}
	}
	if routerPeerNode == nil {
		t.Fatalf("expected router node %s in client peer map, got %d peers", routerNode.hostname, len(peerNodes))
	}
	if !containsPrefix(routerPeerNode.AllowedIPs, smokeMustPrefix(t, targetRoute)) {
		t.Fatalf("expected server peer allowed IPs to include %s, got %v", targetRoute, routerPeerNode.AllowedIPs)
	}
	if !containsPrefix(routerPeerNode.PrimaryRoutes, smokeMustPrefix(t, targetRoute)) {
		t.Fatalf("expected server peer primary routes to include %s, got %v", targetRoute, routerPeerNode.PrimaryRoutes)
	}

	_ = waitForPeerVisible(t, clientNode.socketPath, routerNode.hostname)

	preAcceptOutput, preAcceptErr := runCommand(
		t,
		15*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"ping",
		"--c=1",
		"--timeout=5s",
		targetIP.String(),
	)
	if preAcceptErr == nil {
		t.Skipf("subnet target %s is reachable before accepting routes; output=%s", targetIP, strings.TrimSpace(preAcceptOutput))
	}

	setOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"set",
		"--accept-routes=true",
	)
	if err != nil {
		t.Fatalf("tailscale set --accept-routes=true failed: %v\nstdout/stderr:\n%s", err, setOutput)
	}

	routePing := waitForRoutePing(t, clientNode.socketPath, targetIP.String())
	t.Logf("subnet route ping to %s succeeded: %s", targetIP, strings.TrimSpace(routePing))
}

func TestOfficialClientSubnetRouteLiveUpdateSmoke(t *testing.T) {
	requireSmokeEnv(t)

	targetIP := smokeLocalPrivateIPv4(t)
	targetRoute := netip.PrefixFrom(targetIP, targetIP.BitLen()).String()

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	org.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{user.Name},
		Destinations: []string{targetIP.String() + ":*"},
	}}
	org.AclPolicy.AutoApprovers.Routes = map[string][]string{}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	routerAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(router): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	routerNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "live-update-router"),
		server.serverAddr,
		routerAuthKey.Key,
		"live-update-router",
		"--advertise-routes="+targetRoute,
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "live-update-client"),
		server.serverAddr,
		clientAuthKey.Key,
		"live-update-client",
		"--accept-routes=true",
	)

	_ = waitForNodeRunning(t, routerNode.socketPath)
	_ = waitForNodeRunning(t, clientNode.socketPath)

	routerMachine := waitForMachineWithRouteState(t, server.app, routerNode.hostname, targetRoute, false)
	clientMachine := waitForMachine(t, server.app, clientNode.hostname)
	waitForPeerAbsent(t, clientNode.socketPath, routerNode.hostname)

	preEnableOutput, preEnableErr := runCommand(
		t,
		15*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"ping",
		"--c=1",
		"--timeout=5s",
		targetIP.String(),
	)
	if preEnableErr == nil {
		t.Skipf("subnet target %s is reachable before enabling routes; output=%s", targetIP, strings.TrimSpace(preEnableOutput))
	}

	if err := server.app.enableRoutes(routerMachine, targetRoute); err != nil {
		t.Fatalf("enableRoutes(%s): %v", targetRoute, err)
	}
	_ = waitForMachineWithRouteState(t, server.app, routerNode.hostname, targetRoute, true)
	routerMachine = waitForMachinePrimaryRoute(t, server.app, routerNode.hostname, targetRoute)
	clientMachine = waitForMachine(t, server.app, clientNode.hostname)

	org, err = server.app.GetOrgnaizationByID(routerMachine.User.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(router): %v", err)
	}
	enableSelf, err := server.app.UpdateACLRulesOfOrg(org, &clientMachine.User, clientMachine)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	clientMachine.User.Organization = *org
	peers, _, err := server.app.getValidPeers(clientMachine, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(%s): %v", clientNode.hostname, err)
	}
	visibleServerSide := false
	for _, peer := range peers {
		if peer.ID == routerMachine.ID {
			visibleServerSide = true
			break
		}
	}
	if !visibleServerSide {
		t.Fatalf("expected %s to become visible server-side after enabling %s, peers=%+v", routerNode.hostname, targetRoute, peers)
	}

	peer := waitForPeerCondition(t, clientNode.socketPath, routerNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.InNetworkMap {
			return fmt.Errorf("peer %s not in network map yet: %+v status=%+v", routerNode.hostname, peer, status)
		}
		return nil
	})
	routePing := waitForRoutePing(t, clientNode.socketPath, targetIP.String())
	t.Logf("live subnet route update propagated to %s: peer=%+v ping=%s", clientNode.hostname, peer, strings.TrimSpace(routePing))
}

func containsPrefix(prefixes []netip.Prefix, target netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix == target {
			return true
		}
	}

	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func smokeMustPrefix(t *testing.T, prefix string) netip.Prefix {
	t.Helper()

	parsed, err := netip.ParsePrefix(prefix)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", prefix, err)
	}

	return parsed
}

func waitForMachine(t *testing.T, app *Mirage, hostname string) *Machine {
	t.Helper()

	var machine *Machine
	waitForCondition(t, 30*time.Second, func() error {
		machines, err := app.ListMachines()
		if err != nil {
			return err
		}
		for i := range machines {
			if machines[i].Hostname == hostname {
				machine = &machines[i]
				return nil
			}
		}

		return fmt.Errorf("machine %s not found yet; machines=%+v", hostname, machines)
	})

	return machine
}

func waitForMachineWithRouteState(t *testing.T, app *Mirage, hostname, route string, wantEnabled bool) *Machine {
	t.Helper()

	targetRoute := smokeMustPrefix(t, route)
	var machine *Machine
	waitForCondition(t, 30*time.Second, func() error {
		candidate := waitForMachine(t, app, hostname)
		advertisedRoutes, err := app.GetAdvertisedRoutes(candidate)
		if err != nil {
			return err
		}
		if !containsPrefix(advertisedRoutes, targetRoute) {
			return fmt.Errorf("advertised routes for %s do not include %s yet: %v", hostname, route, advertisedRoutes)
		}
		enabledRoutes, err := app.GetEnabledRoutes(candidate)
		if err != nil {
			return err
		}
		if containsPrefix(enabledRoutes, targetRoute) != wantEnabled {
			return fmt.Errorf("enabled state for %s on %s = %v, routes=%v", route, hostname, containsPrefix(enabledRoutes, targetRoute), enabledRoutes)
		}
		machine = candidate
		return nil
	})

	return machine
}

func waitForMachinePrimaryRoute(t *testing.T, app *Mirage, hostname, route string) *Machine {
	t.Helper()

	targetRoute := smokeMustPrefix(t, route)
	var machine *Machine
	waitForCondition(t, 30*time.Second, func() error {
		candidate := waitForMachine(t, app, hostname)
		node, err := app.toNode(*candidate, candidate.Shared)
		if err != nil {
			return err
		}
		if !containsPrefix(node.PrimaryRoutes, targetRoute) {
			return fmt.Errorf("primary routes for %s do not include %s yet: %v", hostname, route, node.PrimaryRoutes)
		}
		machine = candidate
		return nil
	})

	return machine
}

func TestOfficialClientAuthKeySmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	expiration := time.Now().Add(2 * time.Hour)
	authKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(): %v", err)
	}

	node := startSmokeNode(t, filepath.Join(tempDir, "node-1"), server.serverAddr, authKey.Key, "mirage-smoke")
	_ = waitForNodeRunning(t, node.socketPath)

	waitForCondition(t, 30*time.Second, func() error {
		machines, err := server.app.ListMachines()
		if err != nil {
			return err
		}
		if len(machines) != 1 {
			return fmt.Errorf("expected 1 machine, found %d", len(machines))
		}
		if machines[0].RegisterMethod != RegisterMethodAuthKey {
			return fmt.Errorf("unexpected register method: %s", machines[0].RegisterMethod)
		}
		return nil
	})
}

func TestOfficialClientFunnelSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	sysCfg := &SysConfig{}
	if err := server.app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	listenPort := freeFunnelTestPort(t)
	sysCfg.FunnelCfg.DirectBindAddrs = StringList{"127.0.0.1"}
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{listenPort}
	if err := server.app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	server.app.cfg.FunnelCfg = sysCfg.FunnelCfg
	server.app.requestFunnelRuntimeReload()

	rt := server.app.currentFunnelRuntime()
	if rt == nil {
		t.Fatal("expected funnel runtime to be started")
	}
	rt.getCertFunc = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		host := strings.TrimSpace(hello.ServerName)
		if host == "" {
			host = "localhost"
		}
		cert := newFunnelTestCertificate(t, host)
		return &cert, nil
	}

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	expiration := time.Now().Add(2 * time.Hour)
	authKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(): %v", err)
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf("path=%s query=%s host=%s", r.URL.Path, r.URL.RawQuery, r.Host))
	}))
	defer backend.Close()
	backendPort := backend.Listener.Addr().(*net.TCPAddr).Port

	node := startSmokeNode(t, filepath.Join(tempDir, "funnel-node"), server.serverAddr, authKey.Key, "funnel-node")
	status := waitForNodeRunning(t, node.socketPath)

	funnelOutput, err := runCommand(
		t,
		60*time.Second,
		"tailscale",
		"--socket", node.socketPath,
		"funnel",
		"--bg",
		"--https="+strconv.Itoa(listenPort),
		"localhost:"+strconv.Itoa(backendPort),
	)
	if err != nil {
		t.Fatalf("tailscale funnel failed: %v\nstdout/stderr:\n%s", err, funnelOutput)
	}

	var machine *Machine
	waitForCondition(t, 30*time.Second, func() error {
		candidate, err := smokeMachineByHostname(server.app, node.hostname)
		if err != nil {
			return err
		}
		if !candidate.GetHostInfo().IngressEnabled {
			return fmt.Errorf("machine %s has not reported ingress enabled yet", node.hostname)
		}
		if _, ok := machinePeerAPIAddress(candidate); !ok {
			return fmt.Errorf("machine %s has not reported peerapi service yet", node.hostname)
		}
		machine = candidate
		return nil
	})
	server.app.requestFunnelRuntimeReload()

	domain := strings.TrimSuffix(status.Self.DNSName, ".")
	if machine != nil {
		if resolved := officialFunnelDomainForMachine(machine, server.app.cfg.IPPrefixes, server.app.cfg.FunnelCfg); resolved != "" {
			domain = resolved
		}
	}
	if domain == "" {
		t.Fatal("expected funnel node to have dns name")
	}
	writeSmokeTLSCert(t, node.stateDir, domain)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         domain,
				InsecureSkipVerify: true, //nolint:gosec
				MinVersion:         tls.VersionTLS12,
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			},
			ForceAttemptHTTP2: false,
		},
	}

	var (
		respBody string
		lastErr  error
	)
	for attempt := 0; attempt < 30; attempt++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://%s:%d/hello?x=1", domain, listenPort), nil)
		if err != nil {
			t.Fatalf("http.NewRequest(): %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("ReadAll(response): %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
			time.Sleep(250 * time.Millisecond)
			continue
		}
		respBody = string(body)
		lastErr = nil
		break
	}
	if lastErr != nil {
		t.Fatalf("official funnel request failed: %v\ntailscaled_log:\n%s", lastErr, smokeLogTail(node, 120))
	}

	if got, want := respBody, "path=/hello query=x=1 host="+domain+":"+strconv.Itoa(listenPort); got != want {
		t.Fatalf("official funnel response = %q, want %q", got, want)
	}
}

func TestOfficialClientPeerSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	expiration := time.Now().Add(2 * time.Hour)
	authKeyA, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeA): %v", err)
	}
	authKeyB, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeB): %v", err)
	}

	nodeA := startSmokeNode(t, filepath.Join(tempDir, "node-a"), server.serverAddr, authKeyA.Key, "mirage-a")
	nodeB := startSmokeNode(t, filepath.Join(tempDir, "node-b"), server.serverAddr, authKeyB.Key, "mirage-b")

	statusA := waitForNodeRunning(t, nodeA.socketPath)
	statusB := waitForNodeRunning(t, nodeB.socketPath)

	if len(statusA.TailscaleIPs) == 0 || len(statusB.TailscaleIPs) == 0 {
		t.Fatalf("expected both nodes to have tailscale IPs, got A=%v B=%v", statusA.TailscaleIPs, statusB.TailscaleIPs)
	}

	waitForCondition(t, 15*time.Second, func() error {
		machines, err := server.app.ListMachines()
		if err != nil {
			return err
		}
		var machineA *Machine
		var machineB *Machine
		for i := range machines {
			switch machines[i].Hostname {
			case nodeA.hostname:
				machineA = &machines[i]
			case nodeB.hostname:
				machineB = &machines[i]
			}
		}
		if machineA == nil || machineB == nil {
			return fmt.Errorf("expected both machines in database, got %+v", machines)
		}
		orgA, err := server.app.GetOrgnaizationByID(machineA.User.OrganizationID)
		if err != nil {
			return err
		}
		enableSelfA, err := server.app.UpdateACLRulesOfOrg(orgA, &machineA.User, machineA)
		if err != nil {
			return err
		}
		machineA.User.Organization = *orgA
		peersA, invalidA, err := server.app.getValidPeers(machineA, enableSelfA)
		if err != nil {
			return err
		}
		if len(peersA) == 0 {
			return fmt.Errorf(
				"server-side peers for %s are empty; enableSelf=%v invalid=%v aclRules=%+v machines=%+v",
				nodeA.hostname,
				enableSelfA,
				invalidA,
				orgA.AclRules,
				machines,
			)
		}

		orgB, err := server.app.GetOrgnaizationByID(machineB.User.OrganizationID)
		if err != nil {
			return err
		}
		enableSelfB, err := server.app.UpdateACLRulesOfOrg(orgB, &machineB.User, machineB)
		if err != nil {
			return err
		}
		machineB.User.Organization = *orgB
		peersB, invalidB, err := server.app.getValidPeers(machineB, enableSelfB)
		if err != nil {
			return err
		}
		if len(peersB) == 0 {
			return fmt.Errorf(
				"server-side peers for %s are empty; enableSelf=%v invalid=%v aclRules=%+v machines=%+v",
				nodeB.hostname,
				enableSelfB,
				invalidB,
				orgB.AclRules,
				machines,
			)
		}

		return nil
	})

	peerA := waitForPeerVisible(t, nodeA.socketPath, nodeB.hostname)
	peerB := waitForPeerVisible(t, nodeB.socketPath, nodeA.hostname)
	if len(peerA.TailscaleIPs) == 0 || len(peerB.TailscaleIPs) == 0 {
		t.Fatalf("expected peer records to contain tailscale IPs, got A=%+v B=%+v", peerA, peerB)
	}
	if !peerA.InNetworkMap || !peerB.InNetworkMap {
		t.Fatalf("expected peers to be in network map, got A=%+v B=%+v", peerA, peerB)
	}

	pingA := waitForTailPing(t, nodeA.socketPath, statusB.TailscaleIPs[0])
	pingB := waitForTailPing(t, nodeB.socketPath, statusA.TailscaleIPs[0])
	routePingA := waitForRoutePing(t, nodeA.socketPath, statusB.TailscaleIPs[0])
	routePingB := waitForRoutePing(t, nodeB.socketPath, statusA.TailscaleIPs[0])

	netcheckA := waitForNetcheckReport(t, nodeA.socketPath)
	netcheckB := waitForNetcheckReport(t, nodeB.socketPath)

	peerA = waitForPeerVisible(t, nodeA.socketPath, nodeB.hostname)
	peerB = waitForPeerVisible(t, nodeB.socketPath, nodeA.hostname)

	t.Logf(
		"nodeA peer route: cur_addr=%q relay=%q peer_relay=%q addrs=%v ping=%q route_ping=%q preferred_derp=%d regions=%d",
		peerA.CurAddr,
		peerA.Relay,
		peerA.PeerRelay,
		peerA.Addrs,
		strings.TrimSpace(pingA),
		strings.TrimSpace(routePingA),
		netcheckA.PreferredDERP,
		len(netcheckA.RegionLatency),
	)
	t.Logf(
		"nodeB peer route: cur_addr=%q relay=%q peer_relay=%q addrs=%v ping=%q route_ping=%q preferred_derp=%d regions=%d",
		peerB.CurAddr,
		peerB.Relay,
		peerB.PeerRelay,
		peerB.Addrs,
		strings.TrimSpace(pingB),
		strings.TrimSpace(routePingB),
		netcheckB.PreferredDERP,
		len(netcheckB.RegionLatency),
	)
}

func TestOfficialClientRapidReconnectSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	expiration := time.Now().Add(2 * time.Hour)
	authKeyA, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeA): %v", err)
	}
	authKeyB, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(nodeB): %v", err)
	}

	nodeA, stopA := startSmokeNodeSession(
		t,
		filepath.Join(tempDir, "node-a"),
		server.serverAddr,
		authKeyA.Key,
		"mirage-a",
		true,
	)
	nodeB := startSmokeNode(t, filepath.Join(tempDir, "node-b"), server.serverAddr, authKeyB.Key, "mirage-b")
	t.Cleanup(stopA)

	statusA := waitForNodeRunning(t, nodeA.socketPath)
	statusB := waitForNodeRunning(t, nodeB.socketPath)
	if len(statusA.TailscaleIPs) == 0 || len(statusB.TailscaleIPs) == 0 {
		t.Fatalf("expected both nodes to have tailscale IPs, got A=%v B=%v", statusA.TailscaleIPs, statusB.TailscaleIPs)
	}

	peerA := waitForPeerVisible(t, nodeA.socketPath, nodeB.hostname)
	peerB := waitForPeerVisible(t, nodeB.socketPath, nodeA.hostname)
	if !peerA.Online || !peerB.Online {
		t.Fatalf("expected both peers to be online before reconnect, got A=%+v B=%+v", peerA, peerB)
	}
	if !peerA.InNetworkMap || !peerB.InNetworkMap {
		t.Fatalf("expected both peers to be in network map before reconnect, got A=%+v B=%+v", peerA, peerB)
	}

	pingA := waitForTailPing(t, nodeA.socketPath, statusB.TailscaleIPs[0])
	pingB := waitForTailPing(t, nodeB.socketPath, statusA.TailscaleIPs[0])

	machineA := waitForMachine(t, server.app, nodeA.hostname)
	initialMachineID := machineA.ID
	if !machineA.isOnline() {
		t.Fatalf("expected %s to be online before reconnect, got %+v", nodeA.hostname, machineA)
	}

	stopA()
	if err := os.Remove(nodeA.socketPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove stale socket %s: %v", nodeA.socketPath, err)
	}

	reconnectedNodeA, reconnectStop := startSmokeNodeSession(
		t,
		filepath.Join(tempDir, "node-a"),
		server.serverAddr,
		"",
		"mirage-a",
		false,
	)
	t.Cleanup(reconnectStop)

	reconnectStatusA := waitForNodeRunning(t, reconnectedNodeA.socketPath)
	if len(reconnectStatusA.TailscaleIPs) == 0 {
		t.Fatalf("expected reconnected node A to have tailscale IPs, got %+v", reconnectStatusA)
	}

	waitForCondition(t, 15*time.Second, func() error {
		machine, err := smokeMachineByHostname(server.app, reconnectedNodeA.hostname)
		if err != nil {
			return err
		}
		if machine.ID != initialMachineID {
			return fmt.Errorf("node A changed identity across reconnect: want %d, got %d", initialMachineID, machine.ID)
		}
		if !machine.isOnline() {
			return fmt.Errorf("node A not online after reconnect: %+v", machine)
		}
		return nil
	})

	observePeerStayedOnline(t, nodeB.socketPath, reconnectedNodeA.hostname, rapidReconnectObservationWindow)
	observeMachineStayedOnline(t, server.app, reconnectedNodeA.hostname, initialMachineID, rapidReconnectObservationWindow)

	peerA = waitForPeerVisible(t, reconnectedNodeA.socketPath, nodeB.hostname)
	peerB = waitForPeerVisible(t, nodeB.socketPath, reconnectedNodeA.hostname)
	if !peerA.Online || !peerB.Online {
		t.Fatalf("expected both peers to remain online after reconnect, got A=%+v B=%+v", peerA, peerB)
	}
	if !peerA.InNetworkMap || !peerB.InNetworkMap {
		t.Fatalf("expected both peers to remain in network map after reconnect, got A=%+v B=%+v", peerA, peerB)
	}

	reconnectPingA := waitForTailPing(t, reconnectedNodeA.socketPath, statusB.TailscaleIPs[0])
	reconnectPingB := waitForTailPing(t, nodeB.socketPath, reconnectStatusA.TailscaleIPs[0])

	t.Logf(
		"rapid reconnect preserved peer online state: nodeA_id=%d ping_before=%q ping_after=%q ping_b_before=%q ping_b_after=%q",
		initialMachineID,
		strings.TrimSpace(pingA),
		strings.TrimSpace(reconnectPingA),
		strings.TrimSpace(pingB),
		strings.TrimSpace(reconnectPingB),
	)
}

func TestOfficialClientExitNodeSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	org.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{user.Name},
		Destinations: []string{"autogroup:internet:*"},
	}}
	org.AclPolicy.AutoApprovers.ExitNode = []string{user.Name}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	exitAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(exit): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	exitNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "exit-node"),
		server.serverAddr,
		exitAuthKey.Key,
		"exit-node",
		"--advertise-exit-node",
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "exit-client"),
		server.serverAddr,
		clientAuthKey.Key,
		"exit-client",
		"--accept-routes=true",
	)

	exitStatus := waitForNodeRunning(t, exitNode.socketPath)
	clientStatus := waitForNodeRunning(t, clientNode.socketPath)
	if len(exitStatus.TailscaleIPs) == 0 || len(clientStatus.TailscaleIPs) == 0 {
		t.Fatalf("expected both nodes to have tailscale IPs, got exit=%v client=%v", exitStatus.TailscaleIPs, clientStatus.TailscaleIPs)
	}

	exitMachine := waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV4.String(), true)
	_ = waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV6.String(), true)

	enabledRoutes, err := server.app.GetEnabledRoutes(exitMachine)
	if err != nil {
		t.Fatalf("GetEnabledRoutes(%s): %v", exitNode.hostname, err)
	}
	if !containsPrefix(enabledRoutes, ExitRouteV4) || !containsPrefix(enabledRoutes, ExitRouteV6) {
		t.Fatalf("expected enabled exit routes for %s, got %v", exitNode.hostname, enabledRoutes)
	}

	exitTailNode, err := server.app.toNode(*exitMachine, exitMachine.Shared)
	if err != nil {
		t.Fatalf("toNode(%s): %v", exitNode.hostname, err)
	}
	if !containsPrefix(exitTailNode.AllowedIPs, ExitRouteV4) || !containsPrefix(exitTailNode.AllowedIPs, ExitRouteV6) {
		t.Fatalf("expected exit node allowed IPs to include exit routes, got %v", exitTailNode.AllowedIPs)
	}

	waitForCondition(t, 30*time.Second, func() error {
		clientMachine := waitForMachine(t, server.app, clientNode.hostname)
		org, err := server.app.GetOrgnaizationByID(clientMachine.User.OrganizationID)
		if err != nil {
			return err
		}
		enableSelf, err := server.app.UpdateACLRulesOfOrg(org, &clientMachine.User, clientMachine)
		if err != nil {
			return err
		}
		clientMachine.User.Organization = *org
		peers, invalid, err := server.app.getValidPeers(clientMachine, enableSelf)
		if err != nil {
			return err
		}
		if len(peers) == 0 {
			return fmt.Errorf("server-side peers for %s are empty; enableSelf=%v invalid=%v aclRules=%+v", clientNode.hostname, enableSelf, invalid, org.AclRules)
		}
		for _, peer := range peers {
			if peer.Hostname == exitNode.hostname {
				return nil
			}
		}
		return fmt.Errorf("exit node %s not yet visible to %s; peers=%+v invalid=%v", exitNode.hostname, clientNode.hostname, peers, invalid)
	})

	exitPeer := waitForPeerVisible(t, clientNode.socketPath, exitNode.hostname)
	if !exitPeer.ExitNodeOption {
		t.Fatalf("expected %s to be an exit-node option, got %+v", exitNode.hostname, exitPeer)
	}
	if !containsString(exitPeer.AllowedIPs, ExitRouteV4.String()) || !containsString(exitPeer.AllowedIPs, ExitRouteV6.String()) {
		t.Fatalf("expected peer allowed IPs to include exit routes, got %+v", exitPeer)
	}

	setOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"set",
		"--exit-node="+exitNode.hostname,
	)
	if err != nil {
		t.Fatalf("tailscale set --exit-node failed: %v\nstdout/stderr:\n%s", err, setOutput)
	}

	selectedPeer := waitForPeerCondition(t, clientNode.socketPath, exitNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNode {
			return fmt.Errorf("peer %s not selected as exit node yet: %+v", exitNode.hostname, peer)
		}
		if status.ExitNodeStatus == nil {
			return fmt.Errorf("status missing ExitNodeStatus: %+v", status)
		}
		if status.ExitNodeStatus.ID != peer.ID {
			return fmt.Errorf("exit node ID mismatch: status=%q peer=%q", status.ExitNodeStatus.ID, peer.ID)
		}
		return nil
	})

	clientStatus = waitForNodeRunning(t, clientNode.socketPath)
	if clientStatus.ExitNodeStatus == nil {
		t.Fatalf("expected client status to report selected exit node")
	}
	if clientStatus.ExitNodeStatus.ID != selectedPeer.ID {
		t.Fatalf("expected selected exit node ID %q, got %+v", selectedPeer.ID, clientStatus.ExitNodeStatus)
	}

	debugRules := readSmokePacketFilterRules(t, exitNode.socketPath)
	if !smokePacketFilterRulesEmpty(debugRules) {
		t.Fatalf("expected exit node packet filter rules to stay empty for autogroup:internet, got:\n%s", debugRules)
	}

	peerPing := waitForTailPing(t, clientNode.socketPath, exitStatus.TailscaleIPs[0])
	t.Logf("exit-node peer ping succeeded: %s", strings.TrimSpace(peerPing))

	httpOutput, httpErr := tryHTTPViaProxy(t, clientNode.httpProxyAddr)
	if httpErr != nil {
		clientDiag, clientDiagErr := readSmokeStatus(t, clientNode.socketPath)
		exitDiag, exitDiagErr := readSmokeStatus(t, exitNode.socketPath)
		t.Fatalf(
			"curl via exit proxy %s failed: %v\nstdout/stderr:\n%s\nclient_status_err=%v\nclient_status=%+v\nexit_status_err=%v\nexit_status=%+v\nclient_tailscaled_log:\n%s\nexit_tailscaled_log:\n%s",
			clientNode.httpProxyAddr,
			httpErr,
			httpOutput,
			clientDiagErr,
			clientDiag,
			exitDiagErr,
			exitDiag,
			smokeLogTail(clientNode, 80),
			smokeLogTail(exitNode, 80),
		)
	}
	t.Logf("exit-node proxy egress succeeded via %s: %s", clientNode.httpProxyAddr, strings.TrimSpace(httpOutput))
}

func TestOfficialClientExitNodeSelectedPeerDropsSubnetRoutesSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	org.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{"*"},
		Destinations: []string{"*:*"},
	}}
	if org.AclPolicy.AutoApprovers.Routes == nil {
		org.AclPolicy.AutoApprovers.Routes = make(map[string][]string)
	}
	org.AclPolicy.AutoApprovers.Routes[sharedSmokeSubnetRoute] = []string{user.Name}
	org.AclPolicy.AutoApprovers.ExitNode = []string{user.Name}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	exitAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(exit): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	exitNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "en"),
		server.serverAddr,
		exitAuthKey.Key,
		"exit-node-routes",
		"--advertise-routes="+sharedSmokeSubnetRoute,
		"--advertise-exit-node",
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "ec"),
		server.serverAddr,
		clientAuthKey.Key,
		"exit-client-routes",
		"--accept-routes=true",
	)

	_ = waitForNodeRunning(t, exitNode.socketPath)
	_ = waitForNodeRunning(t, clientNode.socketPath)
	_ = waitForMachineWithRouteState(t, server.app, exitNode.hostname, sharedSmokeSubnetRoute, true)
	_ = waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV4.String(), true)
	_ = waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV6.String(), true)

	preSelectPeer := waitForPeerCondition(t, clientNode.socketPath, exitNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNodeOption {
			return fmt.Errorf("peer %s missing exit-node option before selection: %+v", exitNode.hostname, peer)
		}
		if !containsString(peer.AllowedIPs, sharedSmokeSubnetRoute) {
			return fmt.Errorf("peer %s missing subnet route %s before exit selection: %+v", exitNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if !containsString(peer.PrimaryRoutes, sharedSmokeSubnetRoute) {
			return fmt.Errorf("peer %s missing primary subnet route %s before exit selection: %+v", exitNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		_ = status
		return nil
	})

	setOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"set",
		"--exit-node="+exitNode.hostname,
	)
	if err != nil {
		t.Fatalf("tailscale set --exit-node failed: %v\nstdout/stderr:\n%s", err, setOutput)
	}

	selectedPeer := waitForPeerCondition(t, clientNode.socketPath, exitNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNode {
			return fmt.Errorf("peer %s not selected as exit node yet: %+v", exitNode.hostname, peer)
		}
		if containsString(peer.AllowedIPs, sharedSmokeSubnetRoute) {
			return fmt.Errorf("selected exit peer %s should not retain subnet route %s in AllowedIPs: %+v", exitNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if containsString(peer.PrimaryRoutes, sharedSmokeSubnetRoute) {
			return fmt.Errorf("selected exit peer %s should not retain subnet route %s in PrimaryRoutes: %+v", exitNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if status.ExitNodeStatus == nil {
			return fmt.Errorf("status missing ExitNodeStatus: %+v", status)
		}
		if status.ExitNodeStatus.ID != peer.ID {
			return fmt.Errorf("exit node ID mismatch: status=%q peer=%q", status.ExitNodeStatus.ID, peer.ID)
		}
		return nil
	})
	if preSelectPeer.ID != selectedPeer.ID {
		t.Fatalf("expected selected peer ID %q to match pre-select peer ID %q", selectedPeer.ID, preSelectPeer.ID)
	}

	httpOutput := smokeHTTPViaProxy(t, clientNode.httpProxyAddr)
	t.Logf("exit-node+subnet peer still proxies internet after subnet trim via %s: %s", clientNode.httpProxyAddr, strings.TrimSpace(httpOutput))
}

func TestOfficialClientExitNodeRapidReconnectSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	user, err := server.app.CreateUser("smoke", "Smoke Test", "smoke-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	org, err := server.app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.AclPolicy == nil {
		t.Fatal("expected organization ACL policy to be initialized")
	}
	org.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{user.Name},
		Destinations: []string{"autogroup:internet:*"},
	}}
	org.AclPolicy.AutoApprovers.ExitNode = []string{user.Name}
	if err := server.app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	exitAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(exit): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(user, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	exitNode, stopExit := startSmokeNodeSession(
		t,
		filepath.Join(tempDir, "exit-node"),
		server.serverAddr,
		exitAuthKey.Key,
		"exit-node",
		true,
		"--advertise-exit-node",
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "exit-client"),
		server.serverAddr,
		clientAuthKey.Key,
		"exit-client",
		"--accept-routes=true",
	)
	t.Cleanup(stopExit)

	exitStatus := waitForNodeRunning(t, exitNode.socketPath)
	clientStatus := waitForNodeRunning(t, clientNode.socketPath)
	if len(exitStatus.TailscaleIPs) == 0 || len(clientStatus.TailscaleIPs) == 0 {
		t.Fatalf("expected both nodes to have tailscale IPs, got exit=%v client=%v", exitStatus.TailscaleIPs, clientStatus.TailscaleIPs)
	}

	exitMachine := waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV4.String(), true)
	initialExitMachineID := exitMachine.ID
	_ = waitForMachineWithRouteState(t, server.app, exitNode.hostname, ExitRouteV6.String(), true)

	setOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"set",
		"--exit-node="+exitNode.hostname,
	)
	if err != nil {
		t.Fatalf("tailscale set --exit-node failed: %v\nstdout/stderr:\n%s", err, setOutput)
	}

	selectedPeer := waitForPeerCondition(t, clientNode.socketPath, exitNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNodeOption {
			return fmt.Errorf("peer %s not yet advertised as exit-node option: %+v", exitNode.hostname, peer)
		}
		if !peer.ExitNode {
			return fmt.Errorf("peer %s not yet selected as exit node: %+v", exitNode.hostname, peer)
		}
		if status.ExitNodeStatus == nil {
			return fmt.Errorf("status missing ExitNodeStatus: %+v", status)
		}
		if status.ExitNodeStatus.ID != peer.ID {
			return fmt.Errorf("exit node ID mismatch: status=%q peer=%q", status.ExitNodeStatus.ID, peer.ID)
		}
		return nil
	})

	httpBefore, httpBeforeErr := tryHTTPViaProxy(t, clientNode.httpProxyAddr)
	if httpBeforeErr != nil {
		t.Logf("exit-node proxy not yet ready before reconnect: %v\nstdout/stderr:\n%s", httpBeforeErr, strings.TrimSpace(httpBefore))
	}

	stopExit()
	if err := os.Remove(exitNode.socketPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove stale socket %s: %v", exitNode.socketPath, err)
	}

	reconnectedExitNode, reconnectStop := startSmokeNodeSession(
		t,
		filepath.Join(tempDir, "exit-node"),
		server.serverAddr,
		"",
		"exit-node",
		false,
		"--advertise-exit-node",
	)
	t.Cleanup(reconnectStop)

	reconnectStatus := waitForNodeRunning(t, reconnectedExitNode.socketPath)
	if len(reconnectStatus.TailscaleIPs) == 0 {
		t.Fatalf("expected reconnected exit node to have tailscale IPs, got %+v", reconnectStatus)
	}

	waitForCondition(t, 15*time.Second, func() error {
		machine, err := smokeMachineByHostname(server.app, reconnectedExitNode.hostname)
		if err != nil {
			return err
		}
		if machine.ID != initialExitMachineID {
			return fmt.Errorf("exit node changed identity across reconnect: want %d, got %d", initialExitMachineID, machine.ID)
		}
		if !machine.isOnline() {
			return fmt.Errorf("exit node not online after reconnect: %+v", machine)
		}
		return nil
	})

	observePeerStayedOnline(t, clientNode.socketPath, reconnectedExitNode.hostname, rapidReconnectObservationWindow)
	observeMachineStayedOnline(t, server.app, reconnectedExitNode.hostname, initialExitMachineID, rapidReconnectObservationWindow)

	selectedPeer = waitForPeerCondition(t, clientNode.socketPath, reconnectedExitNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNodeOption {
			return fmt.Errorf("peer %s lost exit-node option after reconnect: %+v", reconnectedExitNode.hostname, peer)
		}
		if !peer.ExitNode {
			return fmt.Errorf("peer %s lost selected exit-node state after reconnect: %+v", reconnectedExitNode.hostname, peer)
		}
		if status.ExitNodeStatus == nil {
			return fmt.Errorf("status missing ExitNodeStatus after reconnect: %+v", status)
		}
		if status.ExitNodeStatus.ID != peer.ID {
			return fmt.Errorf("exit node ID mismatch after reconnect: status=%q peer=%q", status.ExitNodeStatus.ID, peer.ID)
		}
		return nil
	})

	httpAfter, httpAfterErr := runCommand(
		t,
		30*time.Second,
		"curl",
		"--proxy", "http://"+clientNode.httpProxyAddr,
		"--max-time", "15",
		"-fsS",
		"http://1.1.1.1",
	)
	if httpAfterErr != nil {
		t.Logf("exit-node proxy did not recover before test completion: %v\nstdout/stderr:\n%s", httpAfterErr, strings.TrimSpace(httpAfter))
	}
	t.Logf(
		"exit-node rapid reconnect preserved selection: machine_id=%d peer_id=%q http_before=%q http_before_err=%v http_after=%q http_after_err=%v",
		initialExitMachineID,
		selectedPeer.ID,
		strings.TrimSpace(httpBefore),
		httpBeforeErr,
		strings.TrimSpace(httpAfter),
		httpAfterErr,
	)
}

func TestOfficialClientSharedPeerExitAndSubnetSmoke(t *testing.T) {
	requireSmokeEnv(t)

	tempDir := t.TempDir()
	server := startSmokeServer(t, tempDir)

	sourceUser, err := server.app.CreateUser("shared-source", "Shared Source", "shared-source-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(source): %v", err)
	}
	targetUser, err := server.app.CreateUser("shared-target", "Shared Target", "shared-target-org", "Mirage")
	if err != nil {
		t.Fatalf("CreateUser(target): %v", err)
	}

	sourceOrg, err := server.app.GetOrgnaizationByID(sourceUser.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(source): %v", err)
	}
	if sourceOrg.AclPolicy == nil {
		t.Fatal("expected source organization ACL policy to be initialized")
	}
	sourceOrg.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{"*"},
		Destinations: []string{"*:*"},
	}}
	if sourceOrg.AclPolicy.AutoApprovers.Routes == nil {
		sourceOrg.AclPolicy.AutoApprovers.Routes = make(map[string][]string)
	}
	sourceOrg.AclPolicy.AutoApprovers.Routes[sharedSmokeSubnetRoute] = []string{sourceUser.Name}
	sourceOrg.AclPolicy.AutoApprovers.ExitNode = []string{sourceUser.Name}
	if err := server.app.SaveACLPolicyOfOrg(sourceOrg); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(source): %v", err)
	}

	targetOrg, err := server.app.GetOrgnaizationByID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(target): %v", err)
	}
	if targetOrg.AclPolicy == nil {
		t.Fatal("expected target organization ACL policy to be initialized")
	}
	targetOrg.AclPolicy.ACLs = []ACL{{
		Action:       "accept",
		Sources:      []string{"*"},
		Destinations: []string{"*:*"},
	}}
	targetOrg.AclPolicy.AutoApprovers.Routes = map[string][]string{}
	targetOrg.AclPolicy.AutoApprovers.ExitNode = []string{}
	if err := server.app.SaveACLPolicyOfOrg(targetOrg); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(target): %v", err)
	}

	expiration := time.Now().Add(2 * time.Hour)
	routerAuthKey, err := server.app.CreatePreAuthKey(sourceUser, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(router): %v", err)
	}
	clientAuthKey, err := server.app.CreatePreAuthKey(targetUser, false, false, &expiration, nil)
	if err != nil {
		t.Fatalf("CreatePreAuthKey(client): %v", err)
	}

	routerNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "shared-router"),
		server.serverAddr,
		routerAuthKey.Key,
		"shared-router",
		"--advertise-routes="+sharedSmokeSubnetRoute,
		"--advertise-exit-node",
	)
	clientNode := startSmokeNode(
		t,
		filepath.Join(tempDir, "shared-client"),
		server.serverAddr,
		clientAuthKey.Key,
		"shared-client",
		"--accept-routes=true",
	)

	_ = waitForNodeRunning(t, routerNode.socketPath)
	_ = waitForNodeRunning(t, clientNode.socketPath)

	routerMachine := waitForMachineWithRouteState(t, server.app, routerNode.hostname, sharedSmokeSubnetRoute, true)
	_ = waitForMachineWithRouteState(t, server.app, routerNode.hostname, ExitRouteV4.String(), true)
	_ = waitForMachineWithRouteState(t, server.app, routerNode.hostname, ExitRouteV6.String(), true)
	routerMachine = waitForMachinePrimaryRoute(t, server.app, routerNode.hostname, sharedSmokeSubnetRoute)
	clientMachine := waitForMachine(t, server.app, clientNode.hostname)

	share, err := server.app.CreateMachineShare(routerMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := server.app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}

	waitForCondition(t, 30*time.Second, func() error {
		clientMachine = waitForMachine(t, server.app, clientNode.hostname)
		targetOrg, err := server.app.GetOrgnaizationByID(clientMachine.User.OrganizationID)
		if err != nil {
			return err
		}
		enableSelf, err := server.app.UpdateACLRulesOfOrg(targetOrg, &clientMachine.User, clientMachine)
		if err != nil {
			return err
		}
		clientMachine.User.Organization = *targetOrg
		peers, invalid, err := server.app.getValidPeers(clientMachine, enableSelf)
		if err != nil {
			return err
		}
		if len(peers) == 0 {
			return fmt.Errorf("server-side peers for %s are empty; enableSelf=%v invalid=%v aclRules=%+v", clientNode.hostname, enableSelf, invalid, targetOrg.AclRules)
		}
		for _, peer := range peers {
			if peer.Hostname == routerNode.hostname {
				if !peer.Shared {
					return fmt.Errorf("peer %s is visible but not marked shared: %+v", routerNode.hostname, peer)
				}
				return nil
			}
		}
		return fmt.Errorf("shared peer %s not yet visible to %s; peers=%+v invalid=%v", routerNode.hostname, clientNode.hostname, peers, invalid)
	})

	sharedPeer := waitForPeerCondition(t, clientNode.socketPath, routerNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNodeOption {
			return fmt.Errorf("peer %s missing exit-node option: %+v", routerNode.hostname, peer)
		}
		if !containsString(peer.AllowedIPs, ExitRouteV4.String()) || !containsString(peer.AllowedIPs, ExitRouteV6.String()) {
			return fmt.Errorf("peer %s missing exit routes in AllowedIPs: %+v", routerNode.hostname, peer)
		}
		if !containsString(peer.AllowedIPs, sharedSmokeSubnetRoute) {
			return fmt.Errorf("peer %s missing subnet route %s in AllowedIPs: %+v", routerNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if !containsString(peer.PrimaryRoutes, sharedSmokeSubnetRoute) {
			return fmt.Errorf("peer %s missing subnet route %s in PrimaryRoutes: %+v", routerNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		_ = status
		return nil
	})

	waitForCondition(t, 30*time.Second, func() error {
		status, err := readSmokeStatus(t, routerNode.socketPath)
		if err != nil {
			return err
		}
		for _, peer := range status.Peer {
			if peer.HostName == clientNode.hostname {
				if !peer.ShareeNode {
					return fmt.Errorf("expected source-side target peer %s to be hidden sharee node: %+v", clientNode.hostname, peer)
				}
				return nil
			}
		}
		return fmt.Errorf("expected hidden target peer %s in source-side status json; peers=%+v", clientNode.hostname, status.Peer)
	})
	targetStatus := waitForNodeRunning(t, clientNode.socketPath)
	waitForCondition(t, 30*time.Second, func() error {
		status, err := readSmokeStatus(t, routerNode.socketPath)
		if err != nil {
			return err
		}
		for _, peer := range status.Peer {
			if peer.HostName != clientNode.hostname {
				continue
			}
			for _, realIP := range targetStatus.TailscaleIPs {
				if containsString(peer.TailscaleIPs, realIP) {
					return fmt.Errorf("expected hidden target peer %s to be masqueraded, still exposes real IP %s in %+v", clientNode.hostname, realIP, peer)
				}
			}
			return nil
		}

		return fmt.Errorf("expected hidden target peer %s in source-side status json for masquerade check; peers=%+v", clientNode.hostname, status.Peer)
	})

	waitForCondition(t, 30*time.Second, func() error {
		output, err := runCommand(
			t,
			15*time.Second,
			"tailscale",
			"--socket", routerNode.socketPath,
			"status",
		)
		if err != nil {
			return err
		}
		if strings.Contains(output, clientNode.hostname) {
			return fmt.Errorf("expected source-side plain status to hide target peer %s, got:\n%s", clientNode.hostname, output)
		}
		return nil
	})

	// TSMP ping is not a useful quarantine signal here: upstream handles an
	// inbound TSMP ping before the jailed filter runs, so shared source devices
	// can still receive a TSMP pong even when ordinary traffic is blocked.
	assertPingFails(
		t,
		routerNode.socketPath,
		clientNode.hostname,
		"--icmp",
	)

	setOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", clientNode.socketPath,
		"set",
		"--exit-node="+routerNode.hostname,
	)
	if err != nil {
		t.Fatalf("tailscale set --exit-node(shared) failed: %v\nstdout/stderr:\n%s", err, setOutput)
	}

	selectedPeer := waitForPeerCondition(t, clientNode.socketPath, routerNode.hostname, func(peer smokePeerStatus, status smokeStatus) error {
		if !peer.ExitNode {
			return fmt.Errorf("peer %s not selected as exit node yet: %+v", routerNode.hostname, peer)
		}
		if containsString(peer.AllowedIPs, sharedSmokeSubnetRoute) {
			return fmt.Errorf("selected shared exit peer %s should not retain subnet route %s in AllowedIPs: %+v", routerNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if containsString(peer.PrimaryRoutes, sharedSmokeSubnetRoute) {
			return fmt.Errorf("selected shared exit peer %s should not retain subnet route %s in PrimaryRoutes: %+v", routerNode.hostname, sharedSmokeSubnetRoute, peer)
		}
		if status.ExitNodeStatus == nil {
			return fmt.Errorf("status missing ExitNodeStatus: %+v", status)
		}
		if status.ExitNodeStatus.ID != peer.ID {
			return fmt.Errorf("exit node ID mismatch: status=%q peer=%q", status.ExitNodeStatus.ID, peer.ID)
		}
		return nil
	})
	if selectedPeer.ID != sharedPeer.ID {
		t.Fatalf("expected shared peer ID %q to be selected, got %q", sharedPeer.ID, selectedPeer.ID)
	}

	clientStatus := waitForNodeRunning(t, clientNode.socketPath)
	if clientStatus.ExitNodeStatus == nil || clientStatus.ExitNodeStatus.ID != sharedPeer.ID {
		t.Fatalf("expected client status to report selected shared exit node %q, got %+v", sharedPeer.ID, clientStatus.ExitNodeStatus)
	}

	debugRules := readSmokePacketFilterRules(t, routerNode.socketPath)
	t.Logf("shared router packet filter rules: %s", strings.TrimSpace(debugRules))

	httpOutput := smokeHTTPViaProxy(t, clientNode.httpProxyAddr)
	t.Logf("shared exit-node proxy egress succeeded via %s: %s", clientNode.httpProxyAddr, strings.TrimSpace(httpOutput))
}

func TestSmokePacketFilterRulesEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "json empty array",
			raw:  "[]",
			want: true,
		},
		{
			name: "json null",
			raw:  "null",
			want: true,
		},
		{
			name: "debug prefix plus null",
			raw:  "# doing request GET /localapi/v0/debug-packet-filter-rules\nnull\n",
			want: true,
		},
		{
			name: "debug prefix plus empty array",
			raw:  "# doing request GET /localapi/v0/debug-packet-filter-rules\n[]\n",
			want: true,
		},
		{
			name: "non-empty rules",
			raw:  "# doing request GET /localapi/v0/debug-packet-filter-rules\n[{\"SrcIPs\":[\"100.64.0.0/10\"]}]",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smokePacketFilterRulesEmpty(tt.raw); got != tt.want {
				t.Fatalf("smokePacketFilterRulesEmpty(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func readSmokePacketFilterRules(t *testing.T, socketPath string) string {
	t.Helper()

	output, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", socketPath,
		"debug",
		"localapi",
		"/localapi/v0/debug-packet-filter-rules",
	)
	if err != nil {
		t.Fatalf("tailscale debug localapi /localapi/v0/debug-packet-filter-rules failed: %v\nstdout/stderr:\n%s", err, output)
	}

	return output
}

func smokePacketFilterRulesEmpty(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		raw = line
		break
	}

	return raw == "[]" || raw == "null"
}

func smokeLogTail(node *smokeNode, maxLines int) string {
	if node == nil || node.tailscaledLog == nil {
		return ""
	}
	if maxLines <= 0 {
		maxLines = 40
	}

	lines := strings.Split(strings.TrimRight(node.tailscaledLog.String(), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return strings.Join(lines, "\n")
}

func waitForTailscaledLogAddr(t *testing.T, logBuf *strings.Builder, prefix string) string {
	t.Helper()

	var addr string
	waitForCondition(t, 10*time.Second, func() error {
		for _, line := range strings.Split(logBuf.String(), "\n") {
			if idx := strings.Index(line, prefix); idx >= 0 {
				addr = strings.TrimSpace(line[idx+len(prefix):])
				if addr != "" {
					return nil
				}
			}
		}
		return fmt.Errorf("log prefix %q not found yet", prefix)
	})

	return addr
}

func smokeHTTPViaProxy(t *testing.T, proxyAddr string) string {
	t.Helper()

	output, err := runCommand(
		t,
		30*time.Second,
		"curl",
		"--proxy", "http://"+proxyAddr,
		"--max-time", "15",
		"-fsS",
		"http://1.1.1.1",
	)
	if err != nil {
		t.Fatalf("curl via exit proxy %s failed: %v\nstdout/stderr:\n%s", proxyAddr, err, output)
	}

	return output
}

func tryHTTPViaProxy(t *testing.T, proxyAddr string) (string, error) {
	t.Helper()

	return runCommand(
		t,
		30*time.Second,
		"curl",
		"--proxy", "http://"+proxyAddr,
		"--max-time", "15",
		"-fsS",
		"http://1.1.1.1",
	)
}

func waitForPeerCondition(t *testing.T, socketPath string, expectedHostname string, check func(smokePeerStatus, smokeStatus) error) smokePeerStatus {
	t.Helper()

	var matched smokePeerStatus
	waitForCondition(t, 45*time.Second, func() error {
		status, err := readSmokeStatus(t, socketPath)
		if err != nil {
			return err
		}
		for _, candidate := range status.Peer {
			if candidate.HostName == expectedHostname || strings.Contains(candidate.DNSName, expectedHostname) {
				if err := check(candidate, status); err != nil {
					return err
				}
				matched = candidate
				return nil
			}
		}
		return fmt.Errorf("peer %s not visible yet; current peers=%+v self=%+v", expectedHostname, status.Peer, status.Self)
	})

	return matched
}

func observePeerStayedOnline(t *testing.T, socketPath, expectedHostname string, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for {
		status, err := readSmokeStatus(t, socketPath)
		if err != nil {
			t.Fatalf("readSmokeStatus(%s): %v", socketPath, err)
		}
		peer, ok := smokePeerByHostname(status, expectedHostname)
		if !ok {
			t.Fatalf("peer %s disappeared during reconnect observation; status=%+v", expectedHostname, status.Peer)
		}
		if !peer.Online {
			t.Fatalf("peer %s went offline during reconnect observation: %+v status=%+v", expectedHostname, peer, status)
		}
		if !peer.InNetworkMap {
			t.Fatalf("peer %s left the network map during reconnect observation: %+v status=%+v", expectedHostname, peer, status)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func observeMachineStayedOnline(t *testing.T, app *Mirage, hostname string, wantID int64, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for {
		machine, err := smokeMachineByHostname(app, hostname)
		if err != nil {
			t.Fatalf("smokeMachineByHostname(%s): %v", hostname, err)
		}
		if machine.ID != wantID {
			t.Fatalf("machine %s changed identity during reconnect observation: want %d got %d", hostname, wantID, machine.ID)
		}
		if !machine.isOnline() {
			t.Fatalf("machine %s went offline during reconnect observation: %+v", hostname, machine)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func smokePeerByHostname(status smokeStatus, expectedHostname string) (smokePeerStatus, bool) {
	for _, candidate := range status.Peer {
		if candidate.HostName == expectedHostname || strings.Contains(candidate.DNSName, expectedHostname) {
			return candidate, true
		}
	}
	return smokePeerStatus{}, false
}

func smokeMachineByHostname(app *Mirage, hostname string) (*Machine, error) {
	machines, err := app.ListMachines()
	if err != nil {
		return nil, err
	}
	for i := range machines {
		if machines[i].Hostname == hostname {
			return &machines[i], nil
		}
	}
	return nil, fmt.Errorf("machine %s not found yet; machines=%+v", hostname, machines)
}

func openSmokeDB(t *testing.T, path string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(
		sqlite.Open(path+"?_synchronous=1&_journal_mode=WAL"),
		&gorm.Config{
			DisableForeignKeyConstraintWhenMigrating: true,
			Logger:                                   logger.Default.LogMode(logger.Silent),
		},
	)
	if err != nil {
		t.Fatalf("gorm.Open(): %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxIdleTime(time.Hour)

	return db
}

func initSmokeSchema(t *testing.T, db *gorm.DB) {
	t.Helper()

	for _, model := range []any{
		&SysAdmin{},
		&SysConfig{},
		&NaviRegion{},
		&NaviNode{},
		&User{},
		&Route{},
		&Machine{},
		&PreAuthKey{},
		&Organization{},
		&MachineShare{},
		&OrgInvite{},
	} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatalf("AutoMigrate(%T): %v", model, err)
		}
	}
	if err := migrateFunnelTables(db); err != nil {
		t.Fatalf("migrateFunnelTables(): %v", err)
	}
	if err := migrateFlowLogTables(db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}
}

func mustFreeTCPAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	defer ln.Close()

	return ln.Addr().String()
}

func smokeLocalPrivateIPv4(t *testing.T) netip.Addr {
	t.Helper()

	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("Interfaces(): %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr()
			if !ip.Is4() || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip
		}
	}

	t.Skip("no non-loopback private IPv4 available for subnet smoke test")
	return netip.Addr{}
}

func waitForHTTPReady(t *testing.T, rawURL string, timeout time.Duration) {
	t.Helper()

	waitForCondition(t, timeout, func() error {
		resp, err := http.Get(rawURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		return nil
	})
}

func waitForSocketReady(t *testing.T, socketPath string, timeout time.Duration) {
	t.Helper()

	waitForCondition(t, timeout, func() error {
		info, err := os.Stat(socketPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("%s exists but is not a socket", socketPath)
		}
		return nil
	})
}

func waitForTailDaemon(t *testing.T, socketPath string, timeout time.Duration) {
	t.Helper()

	waitForCondition(t, timeout, func() error {
		_, err := runCommand(
			t,
			10*time.Second,
			"tailscale",
			"--socket", socketPath,
			"status",
			"--json",
		)
		return err
	})
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() error) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %v", timeout, lastErr)
}

func runCommand(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("%s timed out", name)
	}

	return string(output), err
}

func requireSmokeEnv(t *testing.T) {
	t.Helper()

	if os.Getenv("MIRAGE_RUN_TAILSCALE_SMOKE") != "1" {
		t.Skip("set MIRAGE_RUN_TAILSCALE_SMOKE=1 to run the official tailscale client smoke tests")
	}
	if _, err := exec.LookPath("tailscale"); err != nil {
		t.Skipf("tailscale not found in PATH: %v", err)
	}
	if _, err := exec.LookPath("tailscaled"); err != nil {
		t.Skipf("tailscaled not found in PATH: %v", err)
	}
}

func startSmokeServer(t *testing.T, tempDir string) *smokeServer {
	t.Helper()

	dbPath := filepath.Join(tempDir, "db.sqlite")
	db := openSmokeDB(t, dbPath)
	initSmokeSchema(t, db)

	serverAddr := mustFreeTCPAddr(t)
	serverKey := key.NewMachine()
	serverKeyText, err := serverKey.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(serverKey): %v", err)
	}

	sysCfg := &SysConfig{
		ServerURL:  serverAddr,
		ServerKey:  string(serverKeyText),
		Addr:       serverAddr,
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.test",
		DerpUrl:    defaultRemoteDERPMapURL,
		DexSecret:  "smoke-secret",
	}
	if err := db.Create(sysCfg).Error; err != nil {
		t.Fatalf("Create(sysCfg): %v", err)
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig(): %v", err)
	}
	app, err := NewMirage(cfg, db)
	if err != nil {
		t.Fatalf("NewMirage(): %v", err)
	}

	ctrlCh := make(chan CtrlMsg, 1)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- app.Serve(ctrlCh)
	}()

	t.Cleanup(func() {
		ctrlCh <- CtrlMsg{Msg: "stop"}
		select {
		case err := <-serverErrCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Logf("Mirage server stop returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Log("timed out waiting for Mirage server shutdown")
		}
	})

	waitForHTTPReady(t, "http://"+serverAddr+"/key?v=131", 30*time.Second)

	return &smokeServer{
		app:        app,
		serverAddr: serverAddr,
	}
}

func startSmokeNode(t *testing.T, tempDir, serverAddr, authKey, hostname string, extraUpArgs ...string) *smokeNode {
	t.Helper()

	node, stop := startSmokeNodeSession(t, tempDir, serverAddr, authKey, hostname, true, extraUpArgs...)
	t.Cleanup(stop)
	return node
}

func startSmokeNodeSession(t *testing.T, tempDir, serverAddr, authKey, hostname string, reset bool, extraUpArgs ...string) (*smokeNode, func()) {
	t.Helper()

	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", tempDir, err)
	}

	socketPath := filepath.Join(tempDir, "tailscaled.sock")
	statePath := filepath.Join(tempDir, "tailscaled.state")
	stateDir := filepath.Join(tempDir, "tailscaled-data")
	var tailscaledLog strings.Builder

	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	daemonCmd := exec.CommandContext(
		daemonCtx,
		"tailscaled",
		"--tun=userspace-networking",
		"--socket", socketPath,
		"--state", statePath,
		"--statedir", stateDir,
		"--port", "0",
		"--verbose", "1",
		"--outbound-http-proxy-listen", "127.0.0.1:0",
	)
	daemonCmd.Env = append(os.Environ(), "TS_DEBUG_ACME_DIRECTORY_URL=https://acme.invalid/directory")
	daemonCmd.Stdout = &tailscaledLog
	daemonCmd.Stderr = &tailscaledLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("failed to start tailscaled for %s: %v", hostname, err)
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			daemonCancel()
			_ = daemonCmd.Wait()
		})
	}
	t.Cleanup(stop)

	waitForSocketReady(t, socketPath, 30*time.Second)
	waitForTailDaemon(t, socketPath, 30*time.Second)
	httpProxyAddr := waitForTailscaledLogAddr(t, &tailscaledLog, "HTTP proxy listening on ")

	loginServer := "http://" + serverAddr
	upArgs := []string{
		"--socket", socketPath,
		"up",
		"--login-server=" + loginServer,
		"--hostname=" + hostname,
		"--accept-dns=false",
		"--netfilter-mode=off",
		"--timeout=60s",
	}
	if authKey != "" {
		upArgs = append(upArgs, "--auth-key="+authKey)
	}
	if reset {
		upArgs = append(upArgs, "--reset")
	}
	upArgs = append(upArgs, extraUpArgs...)
	upOutput, err := runCommand(
		t,
		90*time.Second,
		"tailscale",
		upArgs...,
	)
	if err != nil {
		t.Fatalf("tailscale up failed for %s: %v\nstdout/stderr:\n%s\ntailscaled:\n%s", hostname, err, upOutput, tailscaledLog.String())
	}

	return &smokeNode{
		hostname:      hostname,
		socketPath:    socketPath,
		stateDir:      stateDir,
		httpProxyAddr: httpProxyAddr,
		tailscaledLog: &tailscaledLog,
	}, stop
}

func writeSmokeTLSCert(t *testing.T, stateDir, domain string) {
	t.Helper()

	cert := newFunnelTestCertificate(t, domain)
	if len(cert.Certificate) == 0 {
		t.Fatal("smoke cert missing certificate chain")
	}
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("smoke cert private key type = %T, want *ecdsa.PrivateKey", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey(): %v", err)
	}

	certPEM := make([]byte, 0)
	for _, der := range cert.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certDir := filepath.Join(stateDir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", certDir, err)
	}
	certPath := filepath.Join(certDir, domain+".crt")
	keyPath := filepath.Join(certDir, domain+".key")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", keyPath, err)
	}
}

func readSmokeStatus(t *testing.T, socketPath string) (smokeStatus, error) {
	t.Helper()

	statusOutput, err := runCommand(
		t,
		15*time.Second,
		"tailscale",
		"--socket", socketPath,
		"status",
		"--json",
	)
	if err != nil {
		return smokeStatus{}, fmt.Errorf("tailscale status --json: %w", err)
	}

	var status smokeStatus
	if err := decodeSmokeJSON(statusOutput, &status); err != nil {
		return smokeStatus{}, fmt.Errorf("unmarshal tailscale status: %w", err)
	}

	return status, nil
}

func readSmokeNetcheckReport(t *testing.T, socketPath string) (netcheck.Report, error) {
	t.Helper()

	reportOutput, err := runCommand(
		t,
		30*time.Second,
		"tailscale",
		"--socket", socketPath,
		"netcheck",
		"--format=json",
	)
	if err != nil {
		return netcheck.Report{}, fmt.Errorf("tailscale netcheck --format=json: %w", err)
	}

	var report netcheck.Report
	if err := decodeSmokeJSON(reportOutput, &report); err != nil {
		return netcheck.Report{}, fmt.Errorf("unmarshal tailscale netcheck: %w", err)
	}

	return report, nil
}

func waitForNodeRunning(t *testing.T, socketPath string) smokeStatus {
	t.Helper()

	var status smokeStatus
	waitForCondition(t, 45*time.Second, func() error {
		nextStatus, err := readSmokeStatus(t, socketPath)
		if err != nil {
			return err
		}
		status = nextStatus
		if status.BackendState != "Running" {
			return fmt.Errorf("backend not running yet: %+v", status)
		}
		if len(status.TailscaleIPs) == 0 {
			return fmt.Errorf("tailscale ips not assigned yet: %+v", status)
		}
		return nil
	})

	return status
}

func waitForPeerVisible(t *testing.T, socketPath string, expectedHostname string) smokePeerStatus {
	t.Helper()

	var peer smokePeerStatus
	var lastStatus smokeStatus
	waitForCondition(t, 45*time.Second, func() error {
		status, err := readSmokeStatus(t, socketPath)
		if err != nil {
			return err
		}
		lastStatus = status
		for _, candidate := range status.Peer {
			if candidate.HostName == expectedHostname || strings.Contains(candidate.DNSName, expectedHostname) {
				peer = candidate
				return nil
			}
		}
		return fmt.Errorf("peer %s not visible yet; current peers=%+v self=%+v", expectedHostname, lastStatus.Peer, lastStatus.Self)
	})

	return peer
}

func waitForPeerAbsent(t *testing.T, socketPath string, expectedHostname string) {
	t.Helper()

	waitForCondition(t, 20*time.Second, func() error {
		status, err := readSmokeStatus(t, socketPath)
		if err != nil {
			return err
		}
		for _, candidate := range status.Peer {
			if candidate.HostName == expectedHostname || strings.Contains(candidate.DNSName, expectedHostname) {
				return fmt.Errorf("peer %s still visible: %+v self=%+v", expectedHostname, candidate, status.Self)
			}
		}
		return nil
	})
}

func waitForTailPing(t *testing.T, socketPath string, target string) string {
	t.Helper()

	var successOutput string
	waitForCondition(t, 45*time.Second, func() error {
		output, err := runCommand(
			t,
			15*time.Second,
			"tailscale",
			"--socket", socketPath,
			"ping",
			"--tsmp",
			"--c=1",
			"--timeout=10s",
			"--until-direct=false",
			target,
		)
		if err != nil {
			return fmt.Errorf("tailscale ping %s failed: %w\n%s", target, err, output)
		}
		successOutput = output
		return nil
	})

	return successOutput
}

func waitForNetcheckReport(t *testing.T, socketPath string) netcheck.Report {
	t.Helper()

	var report netcheck.Report
	waitForCondition(t, 60*time.Second, func() error {
		nextReport, err := readSmokeNetcheckReport(t, socketPath)
		if err != nil {
			return err
		}
		report = nextReport
		if report.PreferredDERP == 0 {
			return fmt.Errorf("preferred DERP not selected yet: %+v", report)
		}
		if len(report.RegionLatency) == 0 {
			return fmt.Errorf("DERP latencies not populated yet: %+v", report)
		}
		if !report.IPv4CanSend && !report.IPv6CanSend {
			return fmt.Errorf("netcheck cannot send over either IPv4 or IPv6 yet: %+v", report)
		}
		return nil
	})

	return report
}

func waitForRoutePing(t *testing.T, socketPath string, target string) string {
	t.Helper()

	var successOutput string
	waitForCondition(t, 45*time.Second, func() error {
		output, err := runCommand(
			t,
			20*time.Second,
			"tailscale",
			"--socket", socketPath,
			"ping",
			"--c=10",
			"--timeout=5s",
			target,
		)
		if err != nil {
			return fmt.Errorf("tailscale route ping %s failed: %w\n%s", target, err, output)
		}
		successOutput = output
		return nil
	})

	return successOutput
}

func assertPingFails(t *testing.T, socketPath string, target string, pingFlag string) {
	t.Helper()

	output, err := runCommand(
		t,
		15*time.Second,
		"tailscale",
		"--socket", socketPath,
		"ping",
		pingFlag,
		"--c=1",
		"--timeout=5s",
		target,
	)
	if err == nil {
		t.Fatalf("expected tailscale ping %s %s to fail, but it succeeded:\n%s", pingFlag, target, output)
	}
}

func decodeSmokeJSON(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, "{["); idx > 0 {
		raw = raw[idx:]
	}

	return json.Unmarshal([]byte(raw), out)
}
