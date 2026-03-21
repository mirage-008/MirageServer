package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tailscale.com/net/netcheck"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const smokeSubnetRoute = "10.123.45.0/24"

type smokeServer struct {
	app        *Mirage
	serverAddr string
}

type smokeNode struct {
	hostname   string
	socketPath string
}

type smokeStatus struct {
	BackendState string                     `json:"BackendState"`
	TailscaleIPs []string                   `json:"TailscaleIPs"`
	Self         smokePeerStatus            `json:"Self"`
	Peer         map[string]smokePeerStatus `json:"Peer"`
}

type smokePeerStatus struct {
	HostName      string   `json:"HostName"`
	DNSName       string   `json:"DNSName"`
	TailscaleIPs  []string `json:"TailscaleIPs"`
	PrimaryRoutes []string `json:"PrimaryRoutes"`
	Addrs         []string `json:"Addrs"`
	CurAddr       string   `json:"CurAddr"`
	Relay         string   `json:"Relay"`
	PeerRelay     string   `json:"PeerRelay"`
	Online        bool     `json:"Online"`
	InNetworkMap  bool     `json:"InNetworkMap"`
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
	)
	daemonCmd.Stdout = &tailscaledLog
	daemonCmd.Stderr = &tailscaledLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("failed to start tailscaled for %s: %v", hostname, err)
	}
	t.Cleanup(func() {
		daemonCancel()
		_ = daemonCmd.Wait()
	})

	waitForSocketReady(t, socketPath, 30*time.Second)
	waitForTailDaemon(t, socketPath, 30*time.Second)

	loginServer := "http://" + serverAddr
	upArgs := []string{
		"--socket", socketPath,
		"up",
		"--login-server=" + loginServer,
		"--auth-key=" + authKey,
		"--hostname=" + hostname,
		"--accept-dns=false",
		"--netfilter-mode=off",
		"--timeout=60s",
		"--reset",
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
		hostname:   hostname,
		socketPath: socketPath,
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

func decodeSmokeJSON(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, "{["); idx > 0 {
		raw = raw[idx:]
	}

	return json.Unmarshal([]byte(raw), out)
}
