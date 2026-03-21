package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type ACLRulesData struct {
	Rules []ACLRuleData `json:"rules"`
}

type ACLRuleData struct {
	ID           int      `json:"id"`
	Action       string   `json:"action"`
	Protocol     string   `json:"proto"`
	Sources      []string `json:"src"`
	Destinations []string `json:"dst"`
}

type ACLRuleBody struct {
	Action       string   `json:"action"`
	Protocol     string   `json:"proto"`
	Sources      []string `json:"src"`
	Destinations []string `json:"dst"`
}

type SetACLRuleREQ struct {
	State string      `json:"state"`
	ID    int         `json:"id"`
	Rule  ACLRuleBody `json:"rule"`
}

func aclRuleDataFromACL(id int, rule ACL) ACLRuleData {
	return ACLRuleData{
		ID:           id,
		Action:       rule.Action,
		Protocol:     rule.Protocol,
		Sources:      append([]string(nil), rule.Sources...),
		Destinations: append([]string(nil), rule.Destinations...),
	}
}

func cloneACLRule(rule ACL) ACL {
	return ACL{
		Action:       rule.Action,
		Protocol:     rule.Protocol,
		Sources:      append([]string(nil), rule.Sources...),
		Destinations: append([]string(nil), rule.Destinations...),
	}
}

func cloneACLRules(rules []ACL) []ACL {
	cloned := make([]ACL, 0, len(rules))
	for _, rule := range rules {
		cloned = append(cloned, cloneACLRule(rule))
	}

	return cloned
}

func normalizeACLRuleItems(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		result = append(result, item)
	}

	return result
}

func normalizeACLRule(rule ACLRuleBody) ACL {
	return ACL{
		Action:       strings.ToLower(strings.TrimSpace(rule.Action)),
		Protocol:     strings.ToLower(strings.TrimSpace(rule.Protocol)),
		Sources:      normalizeACLRuleItems(rule.Sources),
		Destinations: normalizeACLRuleItems(rule.Destinations),
	}
}

func validateACLRuleShape(rule ACL) error {
	if rule.Action == "" {
		return fmt.Errorf("规则动作不能为空")
	}
	if rule.Action != "accept" {
		return fmt.Errorf("规则动作无效")
	}
	if len(rule.Sources) == 0 {
		return fmt.Errorf("规则来源不能为空")
	}
	if len(rule.Destinations) == 0 {
		return fmt.Errorf("规则目标不能为空")
	}
	if _, _, err := parseProtocol(rule.Protocol); err != nil {
		return fmt.Errorf("协议无效")
	}

	return nil
}

func aclRuleValidationMessage(err error) string {
	switch {
	case errors.Is(err, errInvalidAction):
		return "规则动作无效"
	case errors.Is(err, errInvalidAutoGroupSelfSource):
		return "autogroup:self 目标只允许来源为用户、用户组、* 或 autogroup:member"
	case errors.Is(err, errInvalidPortFormat):
		return "规则目标格式无效"
	case errors.Is(err, errWildcardIsNeeded):
		return "该协议要求目标端口使用 *"
	case errors.Is(err, errInvalidGroup):
		return "规则中引用了不存在或无效的用户组"
	case errors.Is(err, errInvalidTag):
		return "规则中引用了不存在或无效的标签"
	default:
		return err.Error()
	}
}

func parseACLRuleID(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("规则不存在")
	}

	id, err := strconv.Atoi(raw)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("规则不存在")
	}

	return id, nil
}

func (h *Mirage) validateACLRulesForOrg(org *Organization, user *User, rules []ACL) error {
	for _, rule := range rules {
		if err := validateACLRuleShape(rule); err != nil {
			return err
		}
	}

	policy := ACLPolicy{}
	if org.AclPolicy != nil {
		policy = *org.AclPolicy
	}
	policy.ACLs = cloneACLRules(rules)

	machines, err := h.ListMachinesByOrgID(org.ID)
	if err != nil {
		return err
	}

	stripEmailDomain := false
	if h.cfg != nil {
		stripEmailDomain = h.cfg.OIDC.StripEmaildomain
	}
	_, _, err = h.generateACLRules(machines, user, policy, stripEmailDomain)
	if err != nil {
		return err
	}

	return nil
}

// 接受/admin/api/acls/rules的Get请求，用于查询ACL规则
func (h *Mirage) CAPIGetRules(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	resData := ACLRulesData{Rules: []ACLRuleData{}}
	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || len(org.AclPolicy.ACLs) == 0 {
		h.doAPIResponse(w, "", resData)
		return
	}

	for id, rule := range org.AclPolicy.ACLs {
		resData.Rules = append(resData.Rules, aclRuleDataFromACL(id, rule))
	}

	h.doAPIResponse(w, "", resData)
}

// 接受/admin/api/acls/rules的Post请求，用于创建或更新ACL规则
func (h *Mirage) CAPIPostRules(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	reqData := SetACLRuleREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedRule := normalizeACLRule(reqData.Rule)
	if err := validateACLRuleShape(normalizedRule); err != nil {
		h.doAPIResponse(w, "ACL规则无效:"+err.Error(), nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil {
		org.AclPolicy = &ACLPolicy{}
	}

	candidateRules := cloneACLRules(org.AclPolicy.ACLs)
	updatedID := reqData.ID
	switch reqData.State {
	case "create":
		candidateRules = append(candidateRules, normalizedRule)
		updatedID = len(candidateRules) - 1
	case "update":
		if reqData.ID < 0 || reqData.ID >= len(candidateRules) {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
		candidateRules[reqData.ID] = normalizedRule
	default:
		h.doAPIResponse(w, "不支持的操作", nil)
		return
	}

	if err := h.validateACLRulesForOrg(org, user, candidateRules); err != nil {
		h.doAPIResponse(w, "ACL规则无效:"+aclRuleValidationMessage(err), nil)
		return
	}

	org.AclPolicy.ACLs = candidateRules
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", aclRuleDataFromACL(updatedID, org.AclPolicy.ACLs[updatedID]))
}

// 注销ACLRule执行DELETE方法api/acls/rules/:id
func (h *Mirage) CAPIDelRules(
	w http.ResponseWriter,
	r *http.Request,
) {
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
	if org.AclPolicy == nil || len(org.AclPolicy.ACLs) == 0 {
		h.doAPIResponse(w, "该ACL规则不存在", nil)
		return
	}

	ruleID, err := parseACLRuleID(strings.TrimPrefix(r.URL.Path, "/admin/api/acls/rules/"))
	if err != nil || ruleID >= len(org.AclPolicy.ACLs) {
		h.doAPIResponse(w, "该ACL规则不存在", nil)
		return
	}

	candidateRules := cloneACLRules(org.AclPolicy.ACLs)
	candidateRules = append(candidateRules[:ruleID], candidateRules[ruleID+1:]...)
	org.AclPolicy.ACLs = candidateRules
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", ruleID)
}
