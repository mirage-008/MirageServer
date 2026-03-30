package controller

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

const (
	funnelDNSACMEStateDir      = "dns-acme"
	funnelDNSACMEAccountKey    = "account.key"
	funnelDNSACMEAccountState  = "account.json"
	funnelDNSACMECertsDir      = "certs"
	funnelACMEDirectoryURLEnv  = "MIRAGE_FUNNEL_ACME_DIRECTORY_URL"
	funnelACMEContactEmailEnv  = "MIRAGE_FUNNEL_ACME_CONTACT_EMAIL"
	funnelDNSPropagationWait   = 2 * time.Minute
	funnelDNSPropagationPoll   = 2 * time.Second
	funnelManagedCertReqTimout = 10 * time.Minute
)

type funnelManagedCertRequestResult struct {
	Cert           *tls.Certificate
	ChallengeType  string
	CertificateRef string
	PrivateKeyRef  string
}

type funnelACMEAccountState struct {
	Email        string                 `json:"email,omitempty"`
	Registration *registration.Resource `json:"registration,omitempty"`
}

type funnelACMEUser struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

func (u *funnelACMEUser) GetEmail() string {
	return u.email
}

func (u *funnelACMEUser) GetRegistration() *registration.Resource {
	return u.registration
}

func (u *funnelACMEUser) GetPrivateKey() crypto.PrivateKey {
	return u.privateKey
}

type funnelDNSChallengeProviderAdapter struct {
	ctx      context.Context
	provider managedFunnelDNSChallengeProvider
}

func (a *funnelDNSChallengeProviderAdapter) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return a.provider.UpsertTXTRecord(a.ctx, info.EffectiveFQDN, info.Value)
}

func (a *funnelDNSChallengeProviderAdapter) CleanUp(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return a.provider.DeleteTXTRecord(a.ctx, info.EffectiveFQDN, info.Value)
}

func (a *funnelDNSChallengeProviderAdapter) Timeout() (timeout, interval time.Duration) {
	return funnelDNSPropagationWait, funnelDNSPropagationPoll
}

func funnelACMEDirectoryURL() string {
	if raw := strings.TrimSpace(os.Getenv(funnelACMEDirectoryURLEnv)); raw != "" {
		return raw
	}
	return lego.LEDirectoryProduction
}

func funnelACMEContactEmail(baseDomain string) string {
	if raw := strings.TrimSpace(os.Getenv(funnelACMEContactEmailEnv)); raw != "" {
		return raw
	}
	baseDomain = normalizeManagedFQDN(baseDomain)
	if baseDomain == "" {
		baseDomain = "localhost.invalid"
	}
	return "funnel-acme@" + baseDomain
}

func funnelDNSACMEStatePath(name string) string {
	return AbsolutePathFromConfigPath(filepath.Join(funnelRuntimeStateDir, funnelDNSACMEStateDir, name))
}

func funnelDNSACMECertPaths(host string) (string, string) {
	host = normalizeManagedFQDN(host)
	base := filepath.Join(AbsolutePathFromConfigPath(filepath.Join(funnelRuntimeStateDir, funnelDNSACMEStateDir, funnelDNSACMECertsDir)), host)
	return base + ".crt.pem", base + ".key.pem"
}

func writeFunnelAtomicFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func loadOrCreateFunnelACMEUser(baseDomain string) (*funnelACMEUser, error) {
	keyPath := funnelDNSACMEStatePath(funnelDNSACMEAccountKey)
	statePath := funnelDNSACMEStatePath(funnelDNSACMEAccountState)

	privateKey, err := loadOrCreateFunnelACMEPrivateKey(keyPath)
	if err != nil {
		return nil, err
	}

	state := funnelACMEAccountState{}
	if raw, readErr := os.ReadFile(statePath); readErr == nil {
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, fmt.Errorf("parse dns acme account state: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}

	email := strings.TrimSpace(state.Email)
	if email == "" {
		email = funnelACMEContactEmail(baseDomain)
	}
	return &funnelACMEUser{
		email:        email,
		registration: state.Registration,
		privateKey:   privateKey,
	}, nil
}

func saveFunnelACMEUser(user *funnelACMEUser) error {
	if user == nil {
		return nil
	}
	state := funnelACMEAccountState{
		Email:        user.email,
		Registration: user.registration,
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFunnelAtomicFile(funnelDNSACMEStatePath(funnelDNSACMEAccountState), raw, 0o600)
}

func loadOrCreateFunnelACMEPrivateKey(path string) (crypto.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		return parseFunnelPEMPrivateKey(raw)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	if err := writeFunnelAtomicFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return privateKey, nil
}

func parseFunnelPEMPrivateKey(raw []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("invalid private key pem")
	}
	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err == nil {
		return privateKey, nil
	}
	ecKey, ecErr := x509.ParseECPrivateKey(block.Bytes)
	if ecErr == nil {
		return ecKey, nil
	}
	return nil, err
}

func ensureFunnelACMERegistration(client *lego.Client, user *funnelACMEUser) error {
	if user == nil {
		return fmt.Errorf("dns acme user is required")
	}
	if user.registration != nil && strings.TrimSpace(user.registration.URI) != "" {
		return nil
	}

	reg, err := client.Registration.ResolveAccountByKey()
	if err != nil {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return err
		}
	}
	user.registration = reg
	return saveFunnelACMEUser(user)
}

func loadFunnelManagedCertificate(host string) (*tls.Certificate, string, string, error) {
	certPath, keyPath := funnelDNSACMECertPaths(host)
	if _, err := os.Stat(certPath); err != nil {
		if os.IsNotExist(err) {
			return nil, certPath, keyPath, nil
		}
		return nil, certPath, keyPath, err
	}
	if _, err := os.Stat(keyPath); err != nil {
		if os.IsNotExist(err) {
			return nil, certPath, keyPath, nil
		}
		return nil, certPath, keyPath, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, certPath, keyPath, err
	}
	return &cert, certPath, keyPath, nil
}

func saveFunnelManagedCertificate(host string, resource *certificate.Resource) (*tls.Certificate, string, string, error) {
	if resource == nil {
		return nil, "", "", fmt.Errorf("missing certificate resource")
	}
	certPath, keyPath := funnelDNSACMECertPaths(host)
	if err := writeFunnelAtomicFile(certPath, resource.Certificate, 0o600); err != nil {
		return nil, "", "", err
	}
	if err := writeFunnelAtomicFile(keyPath, resource.PrivateKey, 0o600); err != nil {
		return nil, "", "", err
	}
	cert, err := tls.X509KeyPair(resource.Certificate, resource.PrivateKey)
	if err != nil {
		return nil, "", "", err
	}
	return &cert, certPath, keyPath, nil
}

func (rt *funnelRuntime) obtainManagedCertificateViaDNS(ctx context.Context, host string) (funnelManagedCertRequestResult, error) {
	provider, err := rt.resolveManagedDNSProvider()
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}
	challengeProvider, ok := provider.(managedFunnelDNSChallengeProvider)
	if !ok || challengeProvider == nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, fmt.Errorf("当前 DNS 提供商不支持 DNS-01")
	}

	cfg, err := effectiveFunnelPlatformConfigFromDB(rt.app.db, rt.app.cfg.BaseDomain)
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}
	user, err := loadOrCreateFunnelACMEUser(cfg.ManagedBaseDomain)
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}

	clientCfg := lego.NewConfig(user)
	clientCfg.CADirURL = funnelACMEDirectoryURL()
	client, err := lego.NewClient(clientCfg)
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}
	if err := ensureFunnelACMERegistration(client, user); err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}

	if err := client.Challenge.SetDNS01Provider(&funnelDNSChallengeProviderAdapter{
		ctx:      ctx,
		provider: challengeProvider,
	}); err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}

	cachedCert, certPath, keyPath, err := loadFunnelManagedCertificate(host)
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}

	var resource *certificate.Resource
	if cachedCert != nil {
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
		}
		resource, err = client.Certificate.Renew(certificate.Resource{
			Domain:      host,
			Certificate: certPEM,
			PrivateKey:  keyPEM,
		}, true, false, "")
		if err != nil {
			return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
		}
	} else {
		resource, err = client.Certificate.Obtain(certificate.ObtainRequest{
			Domains: []string{host},
			Bundle:  true,
		})
		if err != nil {
			return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
		}
	}

	cert, certRef, keyRef, err := saveFunnelManagedCertificate(host, resource)
	if err != nil {
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, err
	}
	return funnelManagedCertRequestResult{
		Cert:           cert,
		ChallengeType:  FunnelCertChallengeDNS01,
		CertificateRef: certRef,
		PrivateKeyRef:  keyRef,
	}, nil
}
