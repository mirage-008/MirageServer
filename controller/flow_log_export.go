package controller

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	flowLogExportTargetHTTP          = "http"
	flowLogExportTargetElasticsearch = "elasticsearch"

	flowLogExportDefaultBatchSize = 100
	flowLogExportMaxBatchSize     = 500
	flowLogExportDefaultIndex     = "mirage-flow-logs"
	flowLogExportErrorMaxLength   = 240
)

type FlowLogExportConfig struct {
	Enabled            bool   `json:"enabled"`
	Target             string `json:"target"`
	URL                string `json:"url"`
	APIKey             string `json:"apiKey"`
	Index              string `json:"index"`
	BatchSize          int    `json:"batchSize"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

func normalizeFlowLogExportConfig(cfg FlowLogExportConfig) FlowLogExportConfig {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = flowLogExportDefaultBatchSize
	}
	if batchSize > flowLogExportMaxBatchSize {
		batchSize = flowLogExportMaxBatchSize
	}

	target := strings.ToLower(strings.TrimSpace(cfg.Target))
	if !cfg.Enabled {
		target = ""
	}

	return FlowLogExportConfig{
		Enabled:            cfg.Enabled,
		Target:             target,
		URL:                strings.TrimSpace(cfg.URL),
		APIKey:             strings.TrimSpace(cfg.APIKey),
		Index:              strings.TrimSpace(cfg.Index),
		BatchSize:          batchSize,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
}

func (h *Mirage) exportFlowLogs(ticker *time.Ticker) {
	for {
		select {
		case <-ticker.C:
			if err := h.exportFlowLogsWorker(); err != nil {
				log.Error().Caller().Err(err).Msg("failed to export flow logs")
			}
		case <-h.shutdownChan:
			return
		}
	}
}

func (h *Mirage) exportFlowLogsWorker() error {
	if h == nil || h.db == nil || h.cfg == nil {
		return nil
	}

	exportCfg, err := h.effectiveFlowLogExportConfig()
	if err != nil {
		return err
	}
	if !exportCfg.Enabled {
		return nil
	}

	entries := []FlowLogEntry{}
	if err := h.db.Where("exported_at IS NULL").Order("id asc").Limit(exportCfg.BatchSize).Find(&entries).Error; err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	switch exportCfg.Target {
	case flowLogExportTargetHTTP:
		err = exportFlowLogsHTTP(exportCfg, entries)
	case flowLogExportTargetElasticsearch:
		err = exportFlowLogsElasticsearch(exportCfg, entries)
	default:
		err = fmt.Errorf("unsupported flow log export target %q", exportCfg.Target)
	}
	if err != nil {
		if markErr := h.markFlowLogExportFailure(entries, err); markErr != nil {
			return fmt.Errorf("flow log export failed: %v; additionally failed to mark export failure: %w", err, markErr)
		}
		return err
	}
	return h.markFlowLogExportSuccess(entries)
}

func (h *Mirage) effectiveFlowLogExportConfig() (FlowLogExportConfig, error) {
	cfg := normalizeFlowLogExportConfig(h.cfg.FlowLogCfg.Export)
	if !cfg.Enabled {
		return cfg, nil
	}

	switch cfg.Target {
	case flowLogExportTargetHTTP:
		if cfg.URL == "" {
			return cfg, fmt.Errorf("flow log export url is required for http target")
		}
	case flowLogExportTargetElasticsearch:
		if cfg.URL == "" {
			cfg.URL = strings.TrimSpace(h.cfg.ESURL)
		}
		if cfg.APIKey == "" {
			cfg.APIKey = strings.TrimSpace(h.cfg.ESKey)
		}
		if cfg.URL == "" {
			return cfg, fmt.Errorf("flow log export url is required for elasticsearch target")
		}
		if cfg.Index == "" {
			cfg.Index = flowLogExportDefaultIndex
		}
	default:
		return cfg, fmt.Errorf("unsupported flow log export target %q", cfg.Target)
	}

	if _, err := url.ParseRequestURI(cfg.URL); err != nil {
		return cfg, fmt.Errorf("invalid flow log export url %q: %w", cfg.URL, err)
	}

	return cfg, nil
}

func newFlowLogExportClient(cfg FlowLogExportConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
}

func applyFlowLogExportAuthorization(req *http.Request, target, apiKey string) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return
	}
	if strings.Contains(apiKey, " ") {
		req.Header.Set("Authorization", apiKey)
		return
	}
	if target == flowLogExportTargetElasticsearch {
		req.Header.Set("Authorization", "ApiKey "+apiKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func marshalFlowLogExportPayload(entries []FlowLogEntry) ([]byte, error) {
	payload := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, serializeFlowLogEntry(entry, true))
	}
	return json.Marshal(map[string]any{
		"generatedAt": time.Now().UTC(),
		"count":       len(payload),
		"entries":     payload,
	})
}

func exportFlowLogsHTTP(cfg FlowLogExportConfig, entries []FlowLogEntry) error {
	body, err := marshalFlowLogExportPayload(entries)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	applyFlowLogExportAuthorization(req, cfg.Target, cfg.APIKey)

	res, err := newFlowLogExportClient(cfg).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return fmt.Errorf("http export failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func exportFlowLogsElasticsearch(cfg FlowLogExportConfig, entries []FlowLogEntry) error {
	buf := new(bytes.Buffer)
	encoder := json.NewEncoder(buf)
	for _, entry := range entries {
		if err := encoder.Encode(map[string]any{
			"index": map[string]any{},
		}); err != nil {
			return err
		}
		doc := serializeFlowLogEntry(entry, true)
		doc["@timestamp"] = entry.LoggedAt
		if err := encoder.Encode(doc); err != nil {
			return err
		}
	}

	endpoint := strings.TrimRight(cfg.URL, "/") + "/" + strings.Trim(cfg.Index, "/") + "/_bulk"
	req, err := http.NewRequest(http.MethodPost, endpoint, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	applyFlowLogExportAuthorization(req, cfg.Target, cfg.APIKey)

	res, err := newFlowLogExportClient(cfg).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return fmt.Errorf("elasticsearch export failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(respBody)))
	}

	resp := struct {
		Errors bool `json:"errors"`
	}{}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&resp); err != nil {
		return fmt.Errorf("decode elasticsearch export response: %w", err)
	}
	if resp.Errors {
		return fmt.Errorf("elasticsearch bulk export reported item errors")
	}
	return nil
}

func flowLogEntryIDs(entries []FlowLogEntry) []int64 {
	ids := make([]int64, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func sanitizeFlowLogExportError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > flowLogExportErrorMaxLength {
		msg = msg[:flowLogExportErrorMaxLength]
	}
	return msg
}

func (h *Mirage) markFlowLogExportSuccess(entries []FlowLogEntry) error {
	now := time.Now().UTC()
	return h.db.Model(&FlowLogEntry{}).Where("id IN ?", flowLogEntryIDs(entries)).Updates(map[string]any{
		"last_export_attempt_at": now,
		"exported_at":            now,
		"export_error":           "",
		"export_attempts":        gorm.Expr("export_attempts + ?", 1),
	}).Error
}

func (h *Mirage) markFlowLogExportFailure(entries []FlowLogEntry, exportErr error) error {
	now := time.Now().UTC()
	return h.db.Model(&FlowLogEntry{}).Where("id IN ?", flowLogEntryIDs(entries)).Updates(map[string]any{
		"last_export_attempt_at": now,
		"export_error":           sanitizeFlowLogExportError(exportErr),
		"export_attempts":        gorm.Expr("export_attempts + ?", 1),
	}).Error
}
