package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func (t *noiseServer) NoiseSetDNSHandler(
	writer http.ResponseWriter,
	req *http.Request,
) {
	log.Trace().Msgf("Noise set-dns handler for client %s", req.RemoteAddr)
	if req.Method != http.MethodPost {
		http.Error(writer, "Wrong method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(writer, "Failed to read request", http.StatusBadRequest)
		return
	}

	setDNSRequest := tailcfg.SetDNSRequest{}
	if err := json.Unmarshal(body, &setDNSRequest); err != nil {
		log.Error().Caller().Err(err).Msg("Cannot parse SetDNSRequest")
		http.Error(writer, "Bad request", http.StatusBadRequest)
		return
	}

	machine, err := t.mirage.GetMachineByAnyKey(t.machineKey, setDNSRequest.NodeKey, key.NodePublic{})
	if err != nil {
		log.Warn().Caller().Err(err).Msg("SetDNS request machine lookup failed")
		http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if machine.MachineKey != MachinePublicKeyStripPrefix(t.machineKey) {
		http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := validateSetDNSRequestForMachine(t.mirage, machine, &setDNSRequest); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	provider, err := t.mirage.currentManagedFunnelDNSProvider()
	if err != nil {
		log.Error().Caller().Err(err).Msg("Failed to resolve DNS provider for SetDNS")
		http.Error(writer, "Internal error", http.StatusInternalServerError)
		return
	}
	if provider == nil {
		http.Error(writer, "Funnel DNS provider unavailable", http.StatusServiceUnavailable)
		return
	}

	challengeProvider, ok := provider.(managedFunnelDNSChallengeProvider)
	if !ok {
		http.Error(writer, "Funnel DNS provider does not support ACME challenges", http.StatusNotImplemented)
		return
	}
	if err := challengeProvider.UpsertTXTRecord(req.Context(), setDNSRequest.Name, setDNSRequest.Value); err != nil {
		log.Error().
			Caller().
			Err(err).
			Str("machine", machine.Hostname).
			Str("name", setDNSRequest.Name).
			Msg("Failed to upsert ACME DNS challenge record")
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(writer).Encode(tailcfg.SetDNSResponse{}); err != nil {
		log.Error().Caller().Err(err).Msg("Failed to encode SetDNSResponse")
	}
}

func validateSetDNSRequestForMachine(h *Mirage, machine *Machine, req *tailcfg.SetDNSRequest) error {
	if h == nil || machine == nil || req == nil {
		return fmt.Errorf("invalid set-dns request")
	}
	if req.NodeKey.IsZero() {
		return fmt.Errorf("missing node key")
	}
	if !strings.EqualFold(strings.TrimSpace(req.Type), "TXT") {
		return fmt.Errorf("unsupported dns record type %q", req.Type)
	}
	if strings.TrimSpace(req.Value) == "" {
		return fmt.Errorf("missing dns value")
	}

	expectedName := officialACMEChallengeNameForMachine(machine, h.cfg.IPPrefixes)
	if expectedName == "" {
		return fmt.Errorf("machine is not eligible for funnel cert dns challenges")
	}
	gotName := strings.TrimSuffix(normalizeFunnelBaseDomain(req.Name), ".")
	if gotName != normalizeFunnelBaseDomain(expectedName) {
		return fmt.Errorf("dns name %q is not allowed for this machine", req.Name)
	}
	return nil
}
