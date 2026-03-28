package controller

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func defaultFunnelSyncEndpoint(edgeType string) string {
	switch strings.ToLower(strings.TrimSpace(edgeType)) {
	case FunnelEdgeTypeNavi:
		return "/navi/funnel"
	case "":
		return "/cockpit/api/funnel/edges/server-edge/sync"
	default:
		return "/cockpit/api/funnel/edges/" + strings.ToLower(strings.TrimSpace(edgeType)) + "/sync"
	}
}

func resolveNaviFunnelEdge(tx *gorm.DB, node *NaviNode) (*FunnelEdge, error) {
	if tx == nil {
		return nil, fmt.Errorf("nil funnel db")
	}
	if node == nil {
		return nil, fmt.Errorf("nil navi node")
	}
	edge, err := resolveFunnelEdge(tx, "", node.ID)
	if err != nil {
		return nil, err
	}
	if edge == nil {
		return nil, fmt.Errorf("funnel edge not found for navi %s", node.ID)
	}
	if edge.EdgeType != FunnelEdgeTypeNavi {
		return nil, fmt.Errorf("funnel edge %s is not a navi edge", edge.EdgeNodeID)
	}
	return edge, nil
}

func touchFunnelEdgeLastSeen(tx *gorm.DB, edge *FunnelEdge) error {
	if tx == nil {
		return fmt.Errorf("nil funnel db")
	}
	if edge == nil {
		return fmt.Errorf("nil funnel edge")
	}
	now := time.Now().UTC()
	edge.LastSeen = &now
	if strings.TrimSpace(edge.HealthStatus) == "" {
		edge.HealthStatus = FunnelEdgeHealthUnknown
	}
	return tx.Save(edge).Error
}

func buildNaviFunnelEdgeSyncPayload(tx *gorm.DB, node *NaviNode) (*FunnelEdgeSyncPayload, error) {
	edge, err := resolveNaviFunnelEdge(tx, node)
	if err != nil {
		return nil, err
	}
	return buildFunnelEdgeSyncPayload(tx, edge)
}
