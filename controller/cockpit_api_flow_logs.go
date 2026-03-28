package controller

import (
	"encoding/json"
	"net/http"
)

func (c *Cockpit) GetFlowLogConfig(w http.ResponseWriter, r *http.Request) {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		c.doAPIResponse(w, "获取系统配置失败", nil)
		return
	}
	c.doAPIResponse(w, "", flowLogConfigEnvelope(r, sysCfg.FlowLogCfg))
}

func (c *Cockpit) SetFlowLogConfig(w http.ResponseWriter, r *http.Request) {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		c.doAPIResponse(w, "获取系统配置失败", nil)
		return
	}

	req := FlowLogConfig{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	sysCfg.FlowLogCfg = normalizeFlowLogConfig(req)
	if err := c.db.Save(sysCfg).Error; err != nil {
		c.doAPIResponse(w, "更新系统配置失败", nil)
		return
	}

	if c.serviceState {
		newCfg, err := c.GetSysCfg().toSrvConfig()
		if err != nil {
			c.doAPIResponse(w, "更新系统配置失败", nil)
			return
		}
		c.CtrlChn <- CtrlMsg{
			Msg:    "update-config",
			SysCfg: newCfg,
		}
	}
	c.doAPIResponse(w, "", flowLogConfigEnvelope(r, sysCfg.FlowLogCfg))
}

func (c *Cockpit) GetFlowLogs(w http.ResponseWriter, r *http.Request) {
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	entries, err := queryFlowLogs(c.db, filter)
	if err != nil {
		c.doAPIResponse(w, "读取流量日志失败:"+err.Error(), nil)
		return
	}
	payload := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, serializeFlowLogEntry(entry, filter.IncludePayload))
	}
	c.doAPIResponse(w, "", map[string]any{"entries": payload})
}

func (c *Cockpit) GetFlowLogSummary(w http.ResponseWriter, r *http.Request) {
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		c.doAPIResponse(w, "获取系统配置失败", nil)
		return
	}
	summary, err := summarizeFlowLogs(c.db, filter, sysCfg.FlowLogCfg)
	if err != nil {
		c.doAPIResponse(w, "读取流量日志汇总失败:"+err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", map[string]any{"summary": summary})
}

func (c *Cockpit) ExportFlowLogs(w http.ResponseWriter, r *http.Request) {
	filter, err := flowLogQueryFromRequest(r)
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	entries, err := queryFlowLogs(c.db, filter)
	if err != nil {
		c.doAPIResponse(w, "读取流量日志失败:"+err.Error(), nil)
		return
	}
	if err := writeFlowLogExport(w, entries); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
