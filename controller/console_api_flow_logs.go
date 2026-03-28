package controller

import "net/http"

func (h *Mirage) CAPIGetFlowLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	filter.OrgID = user.OrganizationID
	entries, err := queryFlowLogs(h.db, filter)
	if err != nil {
		h.doAPIResponse(w, "读取流量日志失败:"+err.Error(), nil)
		return
	}
	payload := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, serializeFlowLogEntry(entry, filter.IncludePayload))
	}
	h.doAPIResponse(w, "", map[string]any{"entries": payload})
}

func (h *Mirage) CAPIGetFlowLogSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	filter.OrgID = user.OrganizationID
	summary, err := summarizeFlowLogs(h.db, filter, h.cfg.FlowLogCfg)
	if err != nil {
		h.doAPIResponse(w, "读取流量日志汇总失败:"+err.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", map[string]any{"summary": summary})
}

func (h *Mirage) CAPIExportFlowLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	filter.OrgID = user.OrganizationID
	entries, err := queryFlowLogs(h.db, filter)
	if err != nil {
		h.doAPIResponse(w, "读取流量日志失败:"+err.Error(), nil)
		return
	}
	if err := writeFlowLogExport(w, entries); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
