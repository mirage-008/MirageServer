package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	funnelRemoteEdgeSyncInterval    = 15 * time.Second
	funnelRemoteEdgeFreshnessWindow = 45 * time.Second
)

type funnelRemoteEdgeWorker struct {
	db     *gorm.DB
	edgeID int64

	ctx    context.Context
	cancel context.CancelFunc

	runtime      *funnelRemoteRuntime
	syncInterval time.Duration
	now          func() time.Time

	pullPayload func(context.Context, *FunnelEdge) (*FunnelEdgeSyncPayload, error)

	triggerCh chan struct{}
	wg        sync.WaitGroup
}

func newFunnelRemoteEdgeWorker(db *gorm.DB, edgeID int64, bindAddrs []string) *funnelRemoteEdgeWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &funnelRemoteEdgeWorker{
		db:           db,
		edgeID:       edgeID,
		ctx:          ctx,
		cancel:       cancel,
		syncInterval: funnelRemoteEdgeSyncInterval,
		now:          time.Now,
		triggerCh:    make(chan struct{}, 1),
	}
	worker.runtime = newFunnelRemoteRuntime(ctx, bindAddrs)
	worker.pullPayload = func(ctx context.Context, edge *FunnelEdge) (*FunnelEdgeSyncPayload, error) {
		return buildFunnelEdgeSyncPayload(worker.db.WithContext(ctx), edge)
	}
	return worker
}

func (w *funnelRemoteEdgeWorker) start() {
	w.wg.Add(1)
	go w.run()
	w.requestSync()
}

func (w *funnelRemoteEdgeWorker) close() {
	w.cancel()
	w.wg.Wait()
	if w.runtime != nil {
		w.runtime.close()
	}
}

func (w *funnelRemoteEdgeWorker) requestSync() {
	select {
	case w.triggerCh <- struct{}{}:
	default:
	}
}

func (w *funnelRemoteEdgeWorker) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if err := w.syncOnce(); err != nil {
				log.Warn().Err(err).Int64("edge_id", w.edgeID).Msg("funnel remote edge periodic sync failed")
			}
		case <-w.triggerCh:
			if err := w.syncOnce(); err != nil {
				log.Warn().Err(err).Int64("edge_id", w.edgeID).Msg("funnel remote edge triggered sync failed")
			}
		}
	}
}

func (w *funnelRemoteEdgeWorker) syncOnce() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}

	edge := &FunnelEdge{}
	if err := w.db.WithContext(w.ctx).First(edge, w.edgeID).Error; err != nil {
		return err
	}
	if edge.EdgeType == FunnelEdgeTypeServer {
		return fmt.Errorf("funnel edge %d is server-edge and cannot run remote worker", edge.ID)
	}

	payload, err := w.pullPayload(w.ctx, edge)
	if err != nil {
		_ = w.markHealth(FunnelEdgeHealthUnhealthy)
		return err
	}
	if err := w.runtime.applyPayload(payload); err != nil {
		_ = w.markHealth(FunnelEdgeHealthUnhealthy)
		return err
	}
	if err := w.markHealth(FunnelEdgeHealthHealthy); err != nil {
		return err
	}
	return nil
}

func (w *funnelRemoteEdgeWorker) markHealth(status string) error {
	now := w.now().UTC()
	updates := map[string]any{
		"health_status": stringsToLowerTrim(status),
		"last_seen":     &now,
		"updated_at":    now,
	}
	return w.db.Model(&FunnelEdge{}).Where("id = ?", w.edgeID).Updates(updates).Error
}

func funnelRemoteEdgeIsFresh(edge *FunnelEdge, now time.Time) bool {
	if edge == nil || edge.LastSeen == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return edge.LastSeen.Add(funnelRemoteEdgeFreshnessWindow).After(now)
}

func stringsToLowerTrim(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
