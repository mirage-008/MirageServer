package controller

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
	"tailscale.com/types/logid"
	"tailscale.com/types/netlogtype"
	"tailscale.com/util/zstdframe"
)

const (
	flowLogCollectionTailtraffic = "tailtraffic.log.tailscale.io"
	flowLogMaxUploadBytes        = 1 << 20
	flowLogDefaultLimit          = 50
	flowLogMaxLimit              = 500
	flowLogDefaultRetentionDays  = 30

	flowLogBucketAuto = "auto"
	flowLogBucketNone = "none"
	flowLogBucketHour = "hour"
	flowLogBucketDay  = "day"
)

type FlowLogConfig struct {
	Enabled       bool `json:"enabled"`
	LogExitFlows  bool `json:"logExitFlows"`
	RetentionDays int  `json:"retentionDays"`
}

func (cfg *FlowLogConfig) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*cfg = FlowLogConfig{}
		return nil
	case []byte:
		return json.Unmarshal(v, cfg)
	case string:
		return json.Unmarshal([]byte(v), cfg)
	default:
		return fmt.Errorf("cannot parse flow log config: unexpected data type %T", value)
	}
}

func (cfg FlowLogConfig) Value() (driver.Value, error) {
	bytes, err := json.Marshal(normalizeFlowLogConfig(cfg))
	return string(bytes), err
}

func normalizeFlowLogConfig(cfg FlowLogConfig) FlowLogConfig {
	retentionDays := cfg.RetentionDays
	if retentionDays <= 0 {
		retentionDays = flowLogDefaultRetentionDays
	}
	return FlowLogConfig{
		Enabled:       cfg.Enabled,
		LogExitFlows:  cfg.Enabled && cfg.LogExitFlows,
		RetentionDays: retentionDays,
	}
}

type FlowLogEntry struct {
	ID                 int64  `gorm:"primary_key;unique;not null"`
	StableID           string `gorm:"uniqueIndex"`
	Collection         string `gorm:"index:idx_flow_logs_collection_private,priority:1"`
	PrivateID          string `gorm:"index:idx_flow_logs_collection_private,priority:2"`
	CopyPrivateID      string
	OrgID              int64  `gorm:"index"`
	OrgStableID        string `gorm:"index"`
	OrgName            string
	MachineID          int64  `gorm:"index"`
	MachineStableID    string `gorm:"index"`
	MachineName        string
	UserID             int64  `gorm:"index"`
	UserStableID       string `gorm:"index"`
	UserName           string
	NodeStableID       string    `gorm:"index"`
	StartTime          time.Time `gorm:"index"`
	EndTime            time.Time `gorm:"index"`
	LoggedAt           time.Time `gorm:"index"`
	ClientTime         *time.Time
	HasSubnetTraffic   bool `gorm:"index"`
	HasExitTraffic     bool `gorm:"index"`
	HasPhysicalTraffic bool `gorm:"index"`
	VirtualTxPackets   uint64
	VirtualTxBytes     uint64
	VirtualRxPackets   uint64
	VirtualRxBytes     uint64
	SubnetTxPackets    uint64
	SubnetTxBytes      uint64
	SubnetRxPackets    uint64
	SubnetRxBytes      uint64
	ExitTxPackets      uint64
	ExitTxBytes        uint64
	ExitRxPackets      uint64
	ExitRxBytes        uint64
	PhysicalTxPackets  uint64
	PhysicalTxBytes    uint64
	PhysicalRxPackets  uint64
	PhysicalRxBytes    uint64
	Payload            string
	CreatedAt          time.Time
}

func (entry *FlowLogEntry) BeforeCreate(tx *gorm.DB) error {
	if entry.ID == 0 {
		flakeID, err := snowflake.NewNode(1)
		if err != nil {
			return err
		}
		entry.ID = flakeID.Generate().Int64()
	}
	entry.StableID = GetShortId(entry.ID)
	return nil
}

func migrateFlowLogTables(db *gorm.DB) error {
	return db.AutoMigrate(&FlowLogEntry{})
}

type flowLogEnvelope struct {
	Logtail struct {
		ClientTime time.Time `json:"client_time"`
		ServerTime time.Time `json:"server_time"`
	} `json:"logtail"`
	Logged time.Time `json:"logged"`
	Text   string    `json:"text"`
	netlogtype.Message
}

type flowLogQuery struct {
	OrgID          int64
	MachineID      int64
	Collection     string
	Since          *time.Time
	Until          *time.Time
	HasExit        *bool
	HasSubnet      *bool
	HasPhysical    *bool
	Bucket         string
	Limit          int
	IncludePayload bool
}

type flowLogTrafficTotals struct {
	TxPackets uint64 `json:"txPkts"`
	TxBytes   uint64 `json:"txBytes"`
	RxPackets uint64 `json:"rxPkts"`
	RxBytes   uint64 `json:"rxBytes"`
}

type flowLogSummaryBucket struct {
	Start              time.Time            `json:"start"`
	End                time.Time            `json:"end"`
	EntryCount         int                  `json:"entryCount"`
	HasSubnetTraffic   bool                 `json:"hasSubnetTraffic"`
	HasExitTraffic     bool                 `json:"hasExitTraffic"`
	HasPhysicalTraffic bool                 `json:"hasPhysicalTraffic"`
	VirtualTraffic     flowLogTrafficTotals `json:"virtualTraffic"`
	SubnetTraffic      flowLogTrafficTotals `json:"subnetTraffic"`
	ExitTraffic        flowLogTrafficTotals `json:"exitTraffic"`
	PhysicalTraffic    flowLogTrafficTotals `json:"physicalTraffic"`
}

type flowLogSummary struct {
	BucketMode         string                 `json:"bucketMode"`
	RetentionDays      int                    `json:"retentionDays"`
	EntryCount         int                    `json:"entryCount"`
	Start              *time.Time             `json:"start,omitempty"`
	End                *time.Time             `json:"end,omitempty"`
	HasSubnetTraffic   bool                   `json:"hasSubnetTraffic"`
	HasExitTraffic     bool                   `json:"hasExitTraffic"`
	HasPhysicalTraffic bool                   `json:"hasPhysicalTraffic"`
	VirtualTraffic     flowLogTrafficTotals   `json:"virtualTraffic"`
	SubnetTraffic      flowLogTrafficTotals   `json:"subnetTraffic"`
	ExitTraffic        flowLogTrafficTotals   `json:"exitTraffic"`
	PhysicalTraffic    flowLogTrafficTotals   `json:"physicalTraffic"`
	Buckets            []flowLogSummaryBucket `json:"buckets,omitempty"`
}

func appendNodeCapabilityIfMissing(caps []tailcfg.NodeCapability, capability tailcfg.NodeCapability) []tailcfg.NodeCapability {
	for _, existing := range caps {
		if existing == capability {
			return caps
		}
	}
	return append(caps, capability)
}

func (h *Mirage) flowLogEnabled() bool {
	return h != nil && h.cfg != nil && normalizeFlowLogConfig(h.cfg.FlowLogCfg).Enabled
}

func (h *Mirage) pruneFlowLogs(ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
			if err := h.pruneFlowLogsWorker(); err != nil {
				log.Error().Caller().Err(err).Msg("failed to prune flow logs")
			}
		case <-h.shutdownChan:
			return
		}
	}
}

func (h *Mirage) pruneFlowLogsWorker() error {
	if h == nil || h.db == nil || h.cfg == nil {
		return nil
	}
	cfg := normalizeFlowLogConfig(h.cfg.FlowLogCfg)
	cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays)
	return h.db.Where("end_time < ?", cutoff).Delete(&FlowLogEntry{}).Error
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r != nil && (r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	if r == nil {
		return scheme + "://localhost"
	}
	return scheme + "://" + r.Host
}

func flowLogConfigEnvelope(r *http.Request, cfg FlowLogConfig) map[string]any {
	cfg = normalizeFlowLogConfig(cfg)
	return map[string]any{
		"config":                  cfg,
		"logTargetHint":           requestBaseURL(r),
		"collectorPath":           "/c/{collection}/{privateID}",
		"netlogCollection":        flowLogCollectionTailtraffic,
		"requiresClientLogTarget": true,
		"retentionCutoffHintDays": cfg.RetentionDays,
	}
}

func normalizeFlowLogBucket(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", flowLogBucketAuto:
		return flowLogBucketAuto, nil
	case flowLogBucketNone, flowLogBucketHour, flowLogBucketDay:
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("invalid bucket %q", raw)
	}
}

func parseFlowLogLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return flowLogDefaultLimit, nil
	}
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return flowLogDefaultLimit, nil
	}
	if limit > flowLogMaxLimit {
		limit = flowLogMaxLimit
	}
	return limit, nil
}

func parseFlowLogTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if unixSeconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		t := time.Unix(unixSeconds, 0).UTC()
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

func parseFlowLogBool(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes":
		v := true
		return &v, nil
	case "0", "false", "no":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid bool %q", raw)
	}
}

func flowLogQueryFromRequest(r *http.Request) (flowLogQuery, error) {
	query := r.URL.Query()
	limit, err := parseFlowLogLimit(query.Get("limit"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("limit 解析失败: %w", err)
	}
	since, err := parseFlowLogTime(query.Get("from"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("from 解析失败: %w", err)
	}
	until, err := parseFlowLogTime(query.Get("to"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("to 解析失败: %w", err)
	}
	hasExit, err := parseFlowLogBool(query.Get("has_exit"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("has_exit 解析失败: %w", err)
	}
	hasSubnet, err := parseFlowLogBool(query.Get("has_subnet"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("has_subnet 解析失败: %w", err)
	}
	hasPhysical, err := parseFlowLogBool(query.Get("has_physical"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("has_physical 解析失败: %w", err)
	}
	bucket, err := normalizeFlowLogBucket(query.Get("bucket"))
	if err != nil {
		return flowLogQuery{}, fmt.Errorf("bucket 解析失败: %w", err)
	}

	filter := flowLogQuery{
		Collection:     strings.TrimSpace(query.Get("collection")),
		Since:          since,
		Until:          until,
		HasExit:        hasExit,
		HasSubnet:      hasSubnet,
		HasPhysical:    hasPhysical,
		Bucket:         bucket,
		Limit:          limit,
		IncludePayload: strings.EqualFold(strings.TrimSpace(query.Get("include_payload")), "true") || strings.TrimSpace(query.Get("include_payload")) == "1",
	}

	if machineIDRaw := strings.TrimSpace(query.Get("machine_id")); machineIDRaw != "" {
		machineID, err := strconv.ParseInt(machineIDRaw, 10, 64)
		if err != nil {
			return flowLogQuery{}, fmt.Errorf("machine_id 解析失败: %w", err)
		}
		filter.MachineID = machineID
	}
	if orgIDRaw := strings.TrimSpace(query.Get("org_id")); orgIDRaw != "" {
		orgID, err := strconv.ParseInt(orgIDRaw, 10, 64)
		if err != nil {
			return flowLogQuery{}, fmt.Errorf("org_id 解析失败: %w", err)
		}
		filter.OrgID = orgID
	}

	return filter, nil
}

func decodeFlowLogEntries(body []byte) ([]json.RawMessage, error) {
	body = bytesTrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}
	if body[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, err
		}
		return entries, nil
	}
	var entry json.RawMessage
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, err
	}
	return []json.RawMessage{entry}, nil
}

func bytesTrimSpace(body []byte) []byte {
	return []byte(strings.TrimSpace(string(body)))
}

func (h *Mirage) ensureOrganizationDomainAuditLogID(org *Organization) (string, error) {
	if org == nil {
		return "", fmt.Errorf("nil organization")
	}
	if existing := strings.TrimSpace(org.DomainAuditLogID); existing != "" {
		return existing, nil
	}
	id, err := logid.NewPrivateID()
	if err != nil {
		return "", err
	}
	if err := h.db.Model(&Organization{}).Where("id = ?", org.ID).Update("domain_audit_log_id", id.String()).Error; err != nil {
		return "", err
	}
	org.DomainAuditLogID = id.String()
	return org.DomainAuditLogID, nil
}

func (h *Mirage) ensureMachineDataPlaneAuditLogID(machine *Machine) (string, error) {
	if machine == nil {
		return "", fmt.Errorf("nil machine")
	}
	if existing := strings.TrimSpace(machine.DataPlaneAuditLogID); existing != "" {
		return existing, nil
	}
	id, err := logid.NewPrivateID()
	if err != nil {
		return "", err
	}
	if err := h.db.Model(&Machine{}).Where("id = ?", machine.ID).Update("data_plane_audit_log_id", id.String()).Error; err != nil {
		return "", err
	}
	machine.DataPlaneAuditLogID = id.String()
	return machine.DataPlaneAuditLogID, nil
}

func (h *Mirage) resolveFlowLogSubject(privateID string, nodeStableID tailcfg.StableNodeID) (*Machine, *Organization, error) {
	if h == nil || h.db == nil {
		return nil, nil, fmt.Errorf("flow log db not initialized")
	}

	machine := &Machine{}
	err := h.db.Preload("User").Preload("User.Organization").Where("data_plane_audit_log_id = ?", privateID).First(machine).Error
	if err == nil {
		return machine, &machine.User.Organization, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}

	if nodeStableID != "" {
		if machineID, parseErr := strconv.ParseInt(string(nodeStableID), 10, 64); parseErr == nil {
			machine, err = h.GetMachineByID(machineID)
			if err == nil {
				return machine, &machine.User.Organization, nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, ErrMachineNotFound) {
				return nil, nil, err
			}
		}
	}

	org := &Organization{}
	err = h.db.Where("domain_audit_log_id = ?", privateID).First(org).Error
	if err == nil {
		return nil, org, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}

	return nil, nil, nil
}

func sumFlowLogCounts(items []netlogtype.ConnectionCounts) netlogtype.Counts {
	var sum netlogtype.Counts
	for _, item := range items {
		sum = sum.Add(item.Counts)
	}
	return sum
}

func isNetlogSample(entry flowLogEnvelope) bool {
	if entry.Start.IsZero() || entry.End.IsZero() {
		return false
	}
	return entry.NodeID != "" || len(entry.VirtualTraffic) > 0 || len(entry.SubnetTraffic) > 0 || len(entry.ExitTraffic) > 0 || len(entry.PhysicalTraffic) > 0
}

func (h *Mirage) storeFlowLogBatch(collection, privateID, copyPrivateID string, body []byte, receivedAt time.Time) (int, error) {
	entries, err := decodeFlowLogEntries(body)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	rows := make([]FlowLogEntry, 0, len(entries))
	for _, rawEntry := range entries {
		envelope := flowLogEnvelope{}
		if err := json.Unmarshal(rawEntry, &envelope); err != nil {
			return 0, err
		}
		if !isNetlogSample(envelope) {
			continue
		}

		machine, org, err := h.resolveFlowLogSubject(privateID, envelope.NodeID)
		if err != nil {
			return 0, err
		}
		if machine == nil && org == nil {
			continue
		}

		virtualCounts := sumFlowLogCounts(envelope.VirtualTraffic)
		subnetCounts := sumFlowLogCounts(envelope.SubnetTraffic)
		exitCounts := sumFlowLogCounts(envelope.ExitTraffic)
		physicalCounts := sumFlowLogCounts(envelope.PhysicalTraffic)
		loggedAt := receivedAt.UTC()
		if !envelope.Logged.IsZero() {
			loggedAt = envelope.Logged.UTC()
		} else if !envelope.Logtail.ServerTime.IsZero() {
			loggedAt = envelope.Logtail.ServerTime.UTC()
		}
		var clientTime *time.Time
		if !envelope.Logtail.ClientTime.IsZero() {
			t := envelope.Logtail.ClientTime.UTC()
			clientTime = &t
		}

		row := FlowLogEntry{
			Collection:         collection,
			PrivateID:          privateID,
			CopyPrivateID:      copyPrivateID,
			NodeStableID:       string(envelope.NodeID),
			StartTime:          envelope.Start.UTC(),
			EndTime:            envelope.End.UTC(),
			LoggedAt:           loggedAt,
			ClientTime:         clientTime,
			HasSubnetTraffic:   len(envelope.SubnetTraffic) > 0,
			HasExitTraffic:     len(envelope.ExitTraffic) > 0,
			HasPhysicalTraffic: len(envelope.PhysicalTraffic) > 0,
			VirtualTxPackets:   virtualCounts.TxPackets,
			VirtualTxBytes:     virtualCounts.TxBytes,
			VirtualRxPackets:   virtualCounts.RxPackets,
			VirtualRxBytes:     virtualCounts.RxBytes,
			SubnetTxPackets:    subnetCounts.TxPackets,
			SubnetTxBytes:      subnetCounts.TxBytes,
			SubnetRxPackets:    subnetCounts.RxPackets,
			SubnetRxBytes:      subnetCounts.RxBytes,
			ExitTxPackets:      exitCounts.TxPackets,
			ExitTxBytes:        exitCounts.TxBytes,
			ExitRxPackets:      exitCounts.RxPackets,
			ExitRxBytes:        exitCounts.RxBytes,
			PhysicalTxPackets:  physicalCounts.TxPackets,
			PhysicalTxBytes:    physicalCounts.TxBytes,
			PhysicalRxPackets:  physicalCounts.RxPackets,
			PhysicalRxBytes:    physicalCounts.RxBytes,
			Payload:            string(rawEntry),
		}
		if org != nil {
			row.OrgID = org.ID
			row.OrgStableID = org.StableID
			row.OrgName = org.Name
		}
		if machine != nil {
			row.MachineID = machine.ID
			row.MachineStableID = GetShortId(machine.ID)
			row.MachineName = machine.GivenName
			row.UserID = machine.User.ID
			row.UserStableID = machine.User.StableID
			row.UserName = machine.User.Name
		}
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return 0, nil
	}
	if err := h.db.CreateInBatches(rows, 100).Error; err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (h *Mirage) LogtailCollectionHandler(w http.ResponseWriter, r *http.Request) {
	collection := strings.TrimSpace(mux.Vars(r)["collection"])
	privateID := strings.TrimSpace(mux.Vars(r)["privateID"])
	if collection == "" || privateID == "" {
		http.Error(w, "bad URL", http.StatusBadRequest)
		return
	}
	if _, err := logid.ParsePrivateID(privateID); err != nil {
		http.Error(w, "bad log id", http.StatusBadRequest)
		return
	}
	copyPrivateID := strings.TrimSpace(r.URL.Query().Get("copyId"))
	if copyPrivateID != "" {
		if _, err := logid.ParsePrivateID(copyPrivateID); err != nil {
			http.Error(w, "bad copy log id", http.StatusBadRequest)
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, flowLogMaxUploadBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), ZstdCompression) {
		body, err = zstdframe.AppendDecode(nil, body)
		if err != nil {
			http.Error(w, "decode zstd failed", http.StatusBadRequest)
			return
		}
	}

	if !h.flowLogEnabled() || collection != flowLogCollectionTailtraffic {
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, err := h.storeFlowLogBatch(collection, privateID, copyPrivateID, body, time.Now().UTC()); err != nil {
		log.Error().Caller().Err(err).Str("collection", collection).Msg("failed to store flow logs")
		http.Error(w, "store flow logs failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func queryFlowLogs(db *gorm.DB, filter flowLogQuery) ([]FlowLogEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("nil flow log db")
	}
	tx := applyFlowLogQuery(db.Model(&FlowLogEntry{}), filter, true).Order("start_time desc")
	entries := []FlowLogEntry{}
	if err := tx.Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

func applyFlowLogQuery(tx *gorm.DB, filter flowLogQuery, withLimit bool) *gorm.DB {
	if tx == nil {
		return tx
	}
	if filter.OrgID != 0 {
		tx = tx.Where("org_id = ?", filter.OrgID)
	}
	if filter.MachineID != 0 {
		tx = tx.Where("machine_id = ?", filter.MachineID)
	}
	if filter.Collection != "" {
		tx = tx.Where("collection = ?", filter.Collection)
	}
	if filter.Since != nil {
		tx = tx.Where("end_time >= ?", filter.Since.UTC())
	}
	if filter.Until != nil {
		tx = tx.Where("start_time <= ?", filter.Until.UTC())
	}
	if filter.HasExit != nil {
		tx = tx.Where("has_exit_traffic = ?", *filter.HasExit)
	}
	if filter.HasSubnet != nil {
		tx = tx.Where("has_subnet_traffic = ?", *filter.HasSubnet)
	}
	if filter.HasPhysical != nil {
		tx = tx.Where("has_physical_traffic = ?", *filter.HasPhysical)
	}
	if withLimit && filter.Limit > 0 {
		tx = tx.Limit(filter.Limit)
	}
	return tx
}

func addCounts(dst *flowLogTrafficTotals, txPkts, txBytes, rxPkts, rxBytes uint64) {
	dst.TxPackets += txPkts
	dst.TxBytes += txBytes
	dst.RxPackets += rxPkts
	dst.RxBytes += rxBytes
}

func addEntryToSummary(summary *flowLogSummary, entry FlowLogEntry) {
	summary.EntryCount++
	if summary.Start == nil || entry.StartTime.Before(*summary.Start) {
		start := entry.StartTime
		summary.Start = &start
	}
	if summary.End == nil || entry.EndTime.After(*summary.End) {
		end := entry.EndTime
		summary.End = &end
	}
	summary.HasSubnetTraffic = summary.HasSubnetTraffic || entry.HasSubnetTraffic
	summary.HasExitTraffic = summary.HasExitTraffic || entry.HasExitTraffic
	summary.HasPhysicalTraffic = summary.HasPhysicalTraffic || entry.HasPhysicalTraffic
	addCounts(&summary.VirtualTraffic, entry.VirtualTxPackets, entry.VirtualTxBytes, entry.VirtualRxPackets, entry.VirtualRxBytes)
	addCounts(&summary.SubnetTraffic, entry.SubnetTxPackets, entry.SubnetTxBytes, entry.SubnetRxPackets, entry.SubnetRxBytes)
	addCounts(&summary.ExitTraffic, entry.ExitTxPackets, entry.ExitTxBytes, entry.ExitRxPackets, entry.ExitRxBytes)
	addCounts(&summary.PhysicalTraffic, entry.PhysicalTxPackets, entry.PhysicalTxBytes, entry.PhysicalRxPackets, entry.PhysicalRxBytes)
}

func addEntryToBucket(bucket *flowLogSummaryBucket, entry FlowLogEntry) {
	bucket.EntryCount++
	bucket.HasSubnetTraffic = bucket.HasSubnetTraffic || entry.HasSubnetTraffic
	bucket.HasExitTraffic = bucket.HasExitTraffic || entry.HasExitTraffic
	bucket.HasPhysicalTraffic = bucket.HasPhysicalTraffic || entry.HasPhysicalTraffic
	addCounts(&bucket.VirtualTraffic, entry.VirtualTxPackets, entry.VirtualTxBytes, entry.VirtualRxPackets, entry.VirtualRxBytes)
	addCounts(&bucket.SubnetTraffic, entry.SubnetTxPackets, entry.SubnetTxBytes, entry.SubnetRxPackets, entry.SubnetRxBytes)
	addCounts(&bucket.ExitTraffic, entry.ExitTxPackets, entry.ExitTxBytes, entry.ExitRxPackets, entry.ExitRxBytes)
	addCounts(&bucket.PhysicalTraffic, entry.PhysicalTxPackets, entry.PhysicalTxBytes, entry.PhysicalRxPackets, entry.PhysicalRxBytes)
}

func resolveFlowLogBucketMode(filter flowLogQuery, entries []FlowLogEntry) string {
	if filter.Bucket != "" && filter.Bucket != flowLogBucketAuto {
		return filter.Bucket
	}
	var start, end time.Time
	switch {
	case filter.Since != nil && filter.Until != nil:
		start, end = filter.Since.UTC(), filter.Until.UTC()
	case len(entries) > 0:
		start, end = entries[0].StartTime, entries[0].EndTime
		for _, entry := range entries[1:] {
			if entry.StartTime.Before(start) {
				start = entry.StartTime
			}
			if entry.EndTime.After(end) {
				end = entry.EndTime
			}
		}
	default:
		return flowLogBucketHour
	}
	if end.Sub(start) <= 72*time.Hour {
		return flowLogBucketHour
	}
	return flowLogBucketDay
}

func flowLogBucketBounds(mode string, t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	switch mode {
	case flowLogBucketDay:
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.Add(24 * time.Hour)
	default:
		start := t.Truncate(time.Hour)
		return start, start.Add(time.Hour)
	}
}

func summarizeFlowLogs(db *gorm.DB, filter flowLogQuery, cfg FlowLogConfig) (flowLogSummary, error) {
	cfg = normalizeFlowLogConfig(cfg)
	summary := flowLogSummary{
		BucketMode:    flowLogBucketNone,
		RetentionDays: cfg.RetentionDays,
	}
	if db == nil {
		return summary, fmt.Errorf("nil flow log db")
	}
	entries := []FlowLogEntry{}
	if err := applyFlowLogQuery(db.Model(&FlowLogEntry{}), filter, false).Order("start_time asc").Find(&entries).Error; err != nil {
		return summary, err
	}
	if len(entries) == 0 {
		return summary, nil
	}
	mode := resolveFlowLogBucketMode(filter, entries)
	summary.BucketMode = mode
	buckets := map[time.Time]*flowLogSummaryBucket{}
	for _, entry := range entries {
		addEntryToSummary(&summary, entry)
		if mode == flowLogBucketNone {
			continue
		}
		start, end := flowLogBucketBounds(mode, entry.StartTime)
		bucket := buckets[start]
		if bucket == nil {
			bucket = &flowLogSummaryBucket{Start: start, End: end}
			buckets[start] = bucket
		}
		addEntryToBucket(bucket, entry)
	}
	if mode != flowLogBucketNone {
		keys := make([]time.Time, 0, len(buckets))
		for start := range buckets {
			keys = append(keys, start)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
		summary.Buckets = make([]flowLogSummaryBucket, 0, len(keys))
		for _, start := range keys {
			summary.Buckets = append(summary.Buckets, *buckets[start])
		}
	}
	return summary, nil
}

func serializeFlowLogEntry(entry FlowLogEntry, includePayload bool) map[string]any {
	record := map[string]any{
		"id":            entry.ID,
		"stableId":      entry.StableID,
		"collection":    entry.Collection,
		"privateId":     entry.PrivateID,
		"copyPrivateId": entry.CopyPrivateID,
		"org": map[string]any{
			"id":       entry.OrgID,
			"stableId": entry.OrgStableID,
			"name":     entry.OrgName,
		},
		"machine": map[string]any{
			"id":       entry.MachineID,
			"stableId": entry.MachineStableID,
			"name":     entry.MachineName,
		},
		"user": map[string]any{
			"id":       entry.UserID,
			"stableId": entry.UserStableID,
			"name":     entry.UserName,
		},
		"nodeStableId":       entry.NodeStableID,
		"start":              entry.StartTime,
		"end":                entry.EndTime,
		"loggedAt":           entry.LoggedAt,
		"clientTime":         entry.ClientTime,
		"hasSubnetTraffic":   entry.HasSubnetTraffic,
		"hasExitTraffic":     entry.HasExitTraffic,
		"hasPhysicalTraffic": entry.HasPhysicalTraffic,
		"virtualTraffic": map[string]uint64{
			"txPkts":  entry.VirtualTxPackets,
			"txBytes": entry.VirtualTxBytes,
			"rxPkts":  entry.VirtualRxPackets,
			"rxBytes": entry.VirtualRxBytes,
		},
		"subnetTraffic": map[string]uint64{
			"txPkts":  entry.SubnetTxPackets,
			"txBytes": entry.SubnetTxBytes,
			"rxPkts":  entry.SubnetRxPackets,
			"rxBytes": entry.SubnetRxBytes,
		},
		"exitTraffic": map[string]uint64{
			"txPkts":  entry.ExitTxPackets,
			"txBytes": entry.ExitTxBytes,
			"rxPkts":  entry.ExitRxPackets,
			"rxBytes": entry.ExitRxBytes,
		},
		"physicalTraffic": map[string]uint64{
			"txPkts":  entry.PhysicalTxPackets,
			"txBytes": entry.PhysicalTxBytes,
			"rxPkts":  entry.PhysicalRxPackets,
			"rxBytes": entry.PhysicalRxBytes,
		},
		"createdAt": entry.CreatedAt,
	}
	if includePayload && entry.Payload != "" {
		record["payload"] = json.RawMessage(entry.Payload)
	}
	return record
}

func writeFlowLogExport(w http.ResponseWriter, entries []FlowLogEntry) error {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	encoder := json.NewEncoder(w)
	for _, entry := range entries {
		if err := encoder.Encode(serializeFlowLogEntry(entry, true)); err != nil {
			return err
		}
	}
	return nil
}
