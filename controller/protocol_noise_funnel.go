package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

var errNaviNoiseAuth = errors.New("navi node key mismatch")

func (t *noiseServer) peerMachineKey() key.MachinePublic {
	if t == nil {
		return key.MachinePublic{}
	}
	if t.conn != nil {
		return t.conn.Peer()
	}
	return t.machineKey
}

func (t *noiseServer) lookupAuthedNaviNode(backendLogID string) (*NaviNode, error) {
	if t == nil || t.mirage == nil {
		return nil, fmt.Errorf("noise server not initialized")
	}
	nodeID := strings.TrimSpace(backendLogID)
	if nodeID == "" {
		return nil, fmt.Errorf("missing navi backend log id")
	}
	node := t.mirage.GetNaviNode(nodeID)
	if node == nil {
		return nil, gorm.ErrRecordNotFound
	}
	if node.NaviKey != MachinePublicKeyStripPrefix(t.peerMachineKey()) {
		return nil, errNaviNoiseAuth
	}
	return node, nil
}

func (t *noiseServer) NoiseNaviPullFunnelHandler(
	writer http.ResponseWriter,
	req *http.Request,
) {
	log.Trace().Msgf("Noise FunnelSync handler for Navi %s", req.RemoteAddr)
	if req.Method != http.MethodPost {
		http.Error(writer, "Wrong method", http.StatusMethodNotAllowed)
		return
	}

	body, _ := io.ReadAll(req.Body)
	pullReq := tailcfg.MapRequest{}
	if err := json.Unmarshal(body, &pullReq); err != nil {
		log.Error().
			Caller().
			Err(err).
			Msg("Cannot parse PullFunnelSyncRequest")
		http.Error(writer, "Internal error", http.StatusInternalServerError)
		return
	}
	if pullReq.Hostinfo == nil {
		http.Error(writer, "Missing hostinfo", http.StatusBadRequest)
		return
	}

	node, err := t.lookupAuthedNaviNode(pullReq.Hostinfo.BackendLogID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			log.Warn().Caller().Msgf("Navi node %s not found", pullReq.Hostinfo.BackendLogID)
			http.Error(writer, "Navi node not found", http.StatusNotFound)
		case errors.Is(err, errNaviNoiseAuth):
			log.Error().
				Caller().
				Str("derpID", pullReq.Hostinfo.BackendLogID).
				Msg("Navi node key mismatch during funnel sync")
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		default:
			log.Error().Caller().Err(err).Msg("Failed to resolve Navi node for funnel sync")
			http.Error(writer, "Internal error", http.StatusInternalServerError)
		}
		return
	}

	edge, err := resolveNaviFunnelEdge(t.mirage.db, node)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(writer, "Funnel edge not found", http.StatusNotFound)
			return
		}
		log.Error().Caller().Err(err).Msg("Failed to resolve Navi funnel edge")
		http.Error(writer, "Internal error", http.StatusInternalServerError)
		return
	}
	if err := touchFunnelEdgeLastSeen(t.mirage.db, edge); err != nil {
		log.Error().Caller().Err(err).Msg("Failed to update Navi funnel edge heartbeat")
		http.Error(writer, "Internal error", http.StatusInternalServerError)
		return
	}

	payload, err := buildFunnelEdgeSyncPayload(t.mirage.db, edge)
	if err != nil {
		log.Error().
			Caller().
			Str("func", "NoiseNaviPullFunnelHandler").
			Err(err).
			Msg("Cannot build funnel sync payload")
		http.Error(writer, "Internal error", http.StatusInternalServerError)
		return
	}
	respBody, err := t.mirage.marshalResponse(payload, t.peerMachineKey())
	if err != nil {
		log.Error().
			Caller().
			Str("func", "NoiseNaviPullFunnelHandler").
			Err(err).
			Msg("Cannot encode funnel sync payload")
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write(respBody); err != nil {
		log.Error().
			Caller().
			Err(err).
			Msg("Failed to write funnel sync payload")
	}
}
