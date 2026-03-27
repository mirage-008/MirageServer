package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

type ACLPolicyDocumentData struct {
	Raw              string `json:"raw"`
	SSHEnabled       bool   `json:"sshEnabled"`
	TestsImplemented bool   `json:"testsImplemented"`
}

type SetACLPolicyREQ struct {
	Policy string `json:"policy"`
}

func emptyACLPolicy() ACLPolicy {
	return ACLPolicy{
		Groups:    make(Groups),
		Hosts:     make(Hosts),
		TagOwners: make(TagOwners),
		ACLs:      make([]ACL, 0),
		Tests:     make([]ACLTest, 0),
		AutoApprovers: AutoApprovers{
			Routes:   make(map[string][]string),
			ExitNode: make([]string, 0),
		},
		SSHs: make([]SSH, 0),
	}
}

func parseACLPolicyDocument(raw string) (ACLPolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return emptyACLPolicy(), nil
	}

	ast, err := hujson.Parse([]byte(raw))
	if err != nil {
		return ACLPolicy{}, err
	}
	ast.Standardize()

	policy := ACLPolicy{}
	if err := json.Unmarshal(ast.Pack(), &policy); err != nil {
		return ACLPolicy{}, err
	}

	return policy, nil
}

func renderACLPolicyDocument(policy ACLPolicy) (string, error) {
	buf := bytes.Buffer{}
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(policy); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func normalizeACLTagName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	name = strings.TrimPrefix(name, "tag:")
	if !validateACLHostName(name) {
		return "", fmt.Errorf("标签名称无效")
	}

	return "tag:" + name, nil
}

func normalizeACLTagOwners(owners []string) []string {
	result := make([]string, 0, len(owners))
	seen := make(map[string]struct{}, len(owners))
	for _, owner := range owners {
		owner = strings.TrimSpace(owner)
		if owner == "" {
			continue
		}
		if strings.HasPrefix(owner, "group:") {
			owner = buildACLGroupName(owner)
		}
		if _, ok := seen[owner]; ok {
			continue
		}
		seen[owner] = struct{}{}
		result = append(result, owner)
	}
	sort.Strings(result)

	return result
}

func normalizeACLTest(rule ACLTest) ACLTest {
	return ACLTest{
		Source: strings.TrimSpace(rule.Source),
		Accept: normalizeACLRuleItems(rule.Accept),
		Deny:   normalizeACLRuleItems(rule.Deny),
	}
}

func normalizeACLPolicyDocument(policy ACLPolicy) (ACLPolicy, error) {
	normalized := emptyACLPolicy()

	groupNames := make(map[string]struct{}, len(policy.Groups))
	for groupName, users := range policy.Groups {
		name := normalizeACLGroupName(groupName)
		if !validateACLGroupName(name) {
			return ACLPolicy{}, fmt.Errorf("用户组名称无效")
		}
		groupName = buildACLGroupName(name)
		if _, ok := groupNames[groupName]; ok {
			return ACLPolicy{}, fmt.Errorf("存在重复的用户组: %s", groupName)
		}
		groupNames[groupName] = struct{}{}
		normalized.Groups[groupName] = normalizeACLGroupUsers(users)
	}

	hostNames := make(map[string]struct{}, len(policy.Hosts))
	for hostName, prefix := range policy.Hosts {
		name := normalizeACLHostName(hostName)
		if !validateACLHostName(name) {
			return ACLPolicy{}, fmt.Errorf("主机别名无效")
		}
		if _, ok := hostNames[name]; ok {
			return ACLPolicy{}, fmt.Errorf("存在重复的主机别名: %s", name)
		}
		hostNames[name] = struct{}{}
		normalized.Hosts[name] = prefix.Masked()
	}

	tagNames := make(map[string]struct{}, len(policy.TagOwners))
	for tagName, owners := range policy.TagOwners {
		name, err := normalizeACLTagName(tagName)
		if err != nil {
			return ACLPolicy{}, err
		}
		if _, ok := tagNames[name]; ok {
			return ACLPolicy{}, fmt.Errorf("存在重复的标签: %s", name)
		}
		tagNames[name] = struct{}{}
		normalized.TagOwners[name] = normalizeACLTagOwners(owners)
	}

	for _, rule := range policy.ACLs {
		normalized.ACLs = append(normalized.ACLs, normalizeACLRule(ACLRuleBody{
			Action:       rule.Action,
			Protocol:     rule.Protocol,
			Sources:      rule.Sources,
			Destinations: rule.Destinations,
		}))
	}

	for _, rule := range policy.Tests {
		normalized.Tests = append(normalized.Tests, normalizeACLTest(rule))
	}

	routeNames := make(map[string]struct{}, len(policy.AutoApprovers.Routes))
	for route, approvers := range policy.AutoApprovers.Routes {
		normalizedRoute, err := normalizeAutoApproverRoute(route)
		if err != nil {
			return ACLPolicy{}, fmt.Errorf("自动审批路由无效")
		}
		if _, ok := routeNames[normalizedRoute]; ok {
			return ACLPolicy{}, fmt.Errorf("存在重复的自动审批路由: %s", normalizedRoute)
		}
		routeNames[normalizedRoute] = struct{}{}
		normalized.AutoApprovers.Routes[normalizedRoute] = normalizeAutoApproverAliases(approvers)
	}
	normalized.AutoApprovers.ExitNode = normalizeAutoApproverAliases(policy.AutoApprovers.ExitNode)

	for _, rule := range policy.SSHs {
		normalized.SSHs = append(normalized.SSHs, normalizeSSHRule(rule))
	}

	return normalized, nil
}

func buildOrgUserSet(users []User) map[string]struct{} {
	allowed := make(map[string]struct{}, len(users))
	for _, user := range users {
		allowed[user.Name] = struct{}{}
	}

	return allowed
}

func validateACLPolicyTagOwners(users []User, policy ACLPolicy) error {
	allowedUsers := buildOrgUserSet(users)
	for tagName, owners := range policy.TagOwners {
		for _, owner := range owners {
			switch {
			case strings.HasPrefix(owner, "group:"):
				if _, ok := policy.Groups[owner]; !ok {
					return fmt.Errorf("标签 %s 引用了不存在的用户组: %s", tagName, owner)
				}
			case strings.HasPrefix(owner, "tag:"):
				return fmt.Errorf("标签 %s 的 owner 只允许用户或用户组", tagName)
			case strings.HasPrefix(owner, AutoGroupPrefix):
				return fmt.Errorf("标签 %s 的 owner 不支持自动组", tagName)
			default:
				if _, ok := allowedUsers[owner]; !ok {
					return fmt.Errorf("标签 %s 引用了不存在的用户: %s", tagName, owner)
				}
			}
		}
	}

	return nil
}

func validateACLPolicyAutoApprovers(users []User, machines []Machine, policy ACLPolicy) error {
	allowed := buildAutoApproverAliasSet(users, &policy, machines)
	for route, approvers := range policy.AutoApprovers.Routes {
		if err := validateAutoApproverApprovers(approvers); err != nil {
			return fmt.Errorf("自动审批路由 %s 无效: %s", route, err.Error())
		}
		if err := validateAutoApproverAliases(approvers, allowed); err != nil {
			return fmt.Errorf("自动审批路由 %s 无效: %s", route, err.Error())
		}
	}
	if len(policy.AutoApprovers.ExitNode) > 0 {
		if err := validateAutoApproverApprovers(policy.AutoApprovers.ExitNode); err != nil {
			return fmt.Errorf("出口节点自动审批无效: %s", err.Error())
		}
		if err := validateAutoApproverAliases(policy.AutoApprovers.ExitNode, allowed); err != nil {
			return fmt.Errorf("出口节点自动审批无效: %s", err.Error())
		}
	}

	return nil
}

func (h *Mirage) validateACLPolicyForOrg(org *Organization, user *User, policy ACLPolicy) error {
	orgUsers, err := h.ListOrgUsers(org.ID)
	if err != nil {
		return err
	}
	if err := h.validateACLGroupUsers(org.ID, groupUsersFromPolicy(policy.Groups)); err != nil {
		return err
	}
	if err := validateACLPolicyTagOwners(orgUsers, policy); err != nil {
		return err
	}

	machines, err := h.ListMachinesByOrgID(org.ID)
	if err != nil {
		return err
	}

	if err := validateACLPolicyAutoApprovers(orgUsers, machines, policy); err != nil {
		return err
	}

	stripEmailDomain := false
	if h.cfg != nil {
		stripEmailDomain = h.cfg.OIDC.StripEmaildomain
	}
	if _, _, err := h.generateACLRules(machines, user, policy, stripEmailDomain); err != nil {
		return fmt.Errorf("ACL 规则无效:%s", aclRuleValidationMessage(err))
	}
	if len(policy.SSHs) > 0 {
		if err := h.validateSSHRulesForPolicy(machines, user.ID, policy, policy.SSHs); err != nil {
			return fmt.Errorf("SSH 规则无效:%s", sshRuleValidationMessage(err))
		}
	}

	return nil
}

func groupUsersFromPolicy(groups Groups) []string {
	all := make([]string, 0)
	for _, users := range groups {
		all = append(all, users...)
	}

	return normalizeACLGroupUsers(all)
}

func (h *Mirage) CAPIGetPolicy(
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

	policy := emptyACLPolicy()
	if org.AclPolicy != nil {
		policy, err = normalizeACLPolicyDocument(*org.AclPolicy)
		if err != nil {
			h.doAPIResponse(w, "读取ACL策略失败:"+err.Error(), nil)
			return
		}
	}

	raw, err := renderACLPolicyDocument(policy)
	if err != nil {
		h.doAPIResponse(w, "读取ACL策略失败:"+err.Error(), nil)
		return
	}

	h.doAPIResponse(w, "", ACLPolicyDocumentData{
		Raw:              raw,
		SSHEnabled:       featureEnableSSH(),
		TestsImplemented: false,
	})
}

func (h *Mirage) CAPIPostPolicy(
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

	reqData := SetACLPolicyREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	policy, err := parseACLPolicyDocument(reqData.Policy)
	if err != nil {
		h.doAPIResponse(w, "ACL策略解析失败:"+err.Error(), nil)
		return
	}
	policy, err = normalizeACLPolicyDocument(policy)
	if err != nil {
		h.doAPIResponse(w, "ACL策略无效:"+err.Error(), nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if err := h.validateACLPolicyForOrg(org, user, policy); err != nil {
		h.doAPIResponse(w, "ACL策略无效:"+err.Error(), nil)
		return
	}

	org.AclPolicy = &policy
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	raw, err := renderACLPolicyDocument(policy)
	if err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}

	h.doAPIResponse(w, "", ACLPolicyDocumentData{
		Raw:              raw,
		SSHEnabled:       featureEnableSSH(),
		TestsImplemented: false,
	})
}
