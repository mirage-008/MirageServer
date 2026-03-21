package controller

import (
	"encoding/json"
	"net/http"
	"strings"
)

type SetSSHRuleREQ struct {
	State string `json:"state"`
	ID    int    `json:"id"`
	Rule  SSH    `json:"rule"`
}

func (h *Mirage) CAPIGetSSH(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !featureEnableSSH() {
		h.doAPIResponse(w, "SSH功能未启用", nil)
		return
	}

	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}

	h.doAPIResponse(w, "", sshRuleListResult(sshRulesOrEmpty(org.AclPolicy)))
}

func (h *Mirage) CAPIPostSSH(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !featureEnableSSH() {
		h.doAPIResponse(w, "SSH功能未启用", nil)
		return
	}

	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	reqData := SetSSHRuleREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedRule := sshRuleStored(reqData.Rule)
	if err := validateSSHRuleShape(normalizedRule); err != nil {
		h.doAPIResponse(w, "SSH规则无效:"+err.Error(), nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	sshRuleEnsurePolicy(&org.AclPolicy)

	candidateRules := sshRulesOrEmpty(org.AclPolicy)
	updatedID := reqData.ID
	switch sshRuleStateValue(reqData.State) {
	case "create":
		candidateRules, updatedID = sshRuleCreate(candidateRules, normalizedRule)
	case "update":
		var ok bool
		candidateRules, ok = sshRuleUpdate(candidateRules, reqData.ID, normalizedRule)
		if !ok {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
	default:
		h.doAPIResponse(w, "不支持的操作", nil)
		return
	}

	if err := h.validateSSHRulesForOrg(org, user, candidateRules); err != nil {
		h.doAPIResponse(w, "SSH规则无效:"+sshRuleErr(err), nil)
		return
	}

	org.AclPolicy.SSHs = candidateRules
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	savedRule, ok := sshRuleLookup(org.AclPolicy.SSHs, updatedID)
	if !ok {
		h.doAPIResponse(w, "notfound", nil)
		return
	}

	h.doAPIResponse(w, "", sshRuleResponse(updatedID, savedRule))
}

func (h *Mirage) CAPIDelSSH(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !featureEnableSSH() {
		h.doAPIResponse(w, "SSH功能未启用", nil)
		return
	}

	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || len(org.AclPolicy.SSHs) == 0 {
		h.doAPIResponse(w, "该SSH规则不存在", nil)
		return
	}

	ruleID, err := sshRuleID(strings.TrimPrefix(r.URL.Path, "/admin/api/acls/ssh/"))
	if err != nil {
		h.doAPIResponse(w, "该SSH规则不存在", nil)
		return
	}

	candidateRules, ok := sshRuleDelete(org.AclPolicy.SSHs, ruleID)
	if !ok {
		h.doAPIResponse(w, "该SSH规则不存在", nil)
		return
	}

	org.AclPolicy.SSHs = candidateRules
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", ruleID)
}
