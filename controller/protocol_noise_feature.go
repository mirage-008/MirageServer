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

func (t *noiseServer) NoiseQueryFeatureHandler(
	writer http.ResponseWriter,
	req *http.Request,
) {
	log.Trace().Msgf("Noise feature query handler for client %s", req.RemoteAddr)
	if req.Method != http.MethodPost {
		http.Error(writer, "Wrong method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(writer, "Failed to read request", http.StatusBadRequest)
		return
	}

	queryReq := tailcfg.QueryFeatureRequest{}
	if err := json.Unmarshal(body, &queryReq); err != nil {
		log.Error().Caller().Err(err).Msg("Cannot parse QueryFeatureRequest")
		http.Error(writer, "Bad request", http.StatusBadRequest)
		return
	}

	machine, err := t.mirage.GetMachineByAnyKey(t.machineKey, queryReq.NodeKey, key.NodePublic{})
	if err != nil {
		log.Warn().Caller().Err(err).Msg("QueryFeature machine lookup failed")
		http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if machine.MachineKey != MachinePublicKeyStripPrefix(t.machineKey) {
		http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp, err := t.mirage.queryFeatureResponseForMachine(machine, &queryReq)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(writer).Encode(resp); err != nil {
		log.Error().Caller().Err(err).Msg("Failed to encode QueryFeatureResponse")
	}
}

func (h *Mirage) queryFeatureResponseForMachine(machine *Machine, req *tailcfg.QueryFeatureRequest) (*tailcfg.QueryFeatureResponse, error) {
	if h == nil || machine == nil || req == nil {
		return nil, fmt.Errorf("invalid feature query")
	}

	feature := strings.ToLower(strings.TrimSpace(req.Feature))
	switch feature {
	case "serve":
		if officialServeAvailable(h.cfg.FunnelCfg) {
			return &tailcfg.QueryFeatureResponse{Complete: true}, nil
		}
		return &tailcfg.QueryFeatureResponse{
			Text:       "Serve is not available on this Mirage control plane because HTTPS/Funnel public ports are not configured.",
			ShouldWait: false,
		}, nil
	case "funnel":
		if officialFunnelAvailable(h.cfg.FunnelCfg) {
			return &tailcfg.QueryFeatureResponse{Complete: true}, nil
		}
		return &tailcfg.QueryFeatureResponse{
			Text:       "Funnel is not available on this Mirage control plane because no public Funnel ports are configured.",
			ShouldWait: false,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported feature %q", req.Feature)
	}
}
