package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

type AutoApproversData struct {
	Routes   []AutoApproverRoute `json:"routes"`
	ExitNode []string            `json:"exitNode"`
}

type AutoApproverRoute struct {
	Route     string   `json:"route"`
	Approvers []string `json:"approvers"`
}

type SetAutoApproverRouteREQ struct {
	State         string   `json:"state"`
	Route         string   `json:"route"`
	PreviousRoute string   `json:"previousRoute"`
	Approvers     []string `json:"approvers"`
}

type SetAutoApproverExitNodeREQ struct {
	Approvers []string `json:"approvers"`
}

func cloneStringSlice(items []string) []string {
	return append([]string(nil), items...)
}

func cloneAutoApproverRoutes(routes map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(routes))
	for route, approvers := range routes {
		cloned[route] = cloneStringSlice(approvers)
	}

	return cloned
}

func normalizeAutoApproverAliases(aliases []string) []string {
	result := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, alias)
	}
	sort.Strings(result)

	return result
}

func normalizeAutoApproverRoute(route string) (string, error) {
	route = strings.TrimSpace(route)
	prefix, err := netip.ParsePrefix(route)
	if err != nil {
		return "", err
	}

	return prefix.Masked().String(), nil
}

func parseAutoApproverPathSegment(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("notfound")
	}

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return "", fmt.Errorf("notfound")
	}

	return decoded, nil
}

func validateAutoApproverAliasShape(alias string) bool {
	if alias == AutoGroupSelf || alias == AutoGroupMember || alias == AutoGroupOwner {
		return true
	}
	if strings.HasPrefix(alias, "group:") || strings.HasPrefix(alias, "tag:") {
		return true
	}
	if strings.HasPrefix(alias, AutoGroupPrefix) {
		return false
	}
	if strings.Contains(alias, "/") {
		return false
	}

	return strings.TrimSpace(alias) != ""
}

func autoApproverAliasHint(alias string) string {
	if strings.HasPrefix(alias, "group:") {
		return fmt.Sprintf("审批人中包含不存在的用户组: %s", alias)
	}
	if strings.HasPrefix(alias, "tag:") {
		return fmt.Sprintf("审批人中包含不存在的标签: %s", alias)
	}
	if strings.HasPrefix(alias, AutoGroupPrefix) {
		return fmt.Sprintf("审批人中包含不支持的自动组: %s", alias)
	}

	return fmt.Sprintf("审批人中包含未知别名: %s", alias)
}

func validateAutoApproverApprovers(approvers []string) error {
	if len(approvers) == 0 {
		return fmt.Errorf("审批人不能为空")
	}
	for _, approver := range approvers {
		if !validateAutoApproverAliasShape(approver) {
			return fmt.Errorf("%s", autoApproverAliasHint(approver))
		}
	}

	return nil
}

func addAutoApproverAliasVariants(allowed map[string]struct{}, aliases []string) {
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if strings.HasPrefix(alias, "tag:") {
			allowed[alias] = struct{}{}
		}
	}
}

func buildAutoApproverAliasSet(users []User, aclPolicy *ACLPolicy, machines []Machine) map[string]struct{} {
	allowed := map[string]struct{}{
		AutoGroupSelf:   {},
		AutoGroupMember: {},
		AutoGroupOwner:  {},
	}
	for _, user := range users {
		allowed[user.Name] = struct{}{}
	}
	if aclPolicy != nil {
		for groupName := range aclPolicy.Groups {
			allowed[groupName] = struct{}{}
		}
		for tagName := range aclPolicy.TagOwners {
			allowed[tagName] = struct{}{}
		}
	}
	for _, machine := range machines {
		addAutoApproverAliasVariants(allowed, machine.ForcedTags)
	}

	return allowed
}

func validateAutoApproverAliases(aliases []string, allowed map[string]struct{}) error {
	for _, alias := range aliases {
		if !validateAutoApproverAliasShape(alias) {
			return fmt.Errorf("%s", autoApproverAliasHint(alias))
		}
		if _, ok := allowed[alias]; !ok {
			return fmt.Errorf("%s", autoApproverAliasHint(alias))
		}
	}

	return nil
}

func (h *Mirage) validateAutoApproverAliasesForOrg(org *Organization, aliases []string) error {
	if len(aliases) == 0 {
		return nil
	}

	users, err := h.ListOrgUsers(org.ID)
	if err != nil {
		return err
	}
	machines, err := h.ListMachinesByOrgID(org.ID)
	if err != nil {
		return err
	}

	allowed := buildAutoApproverAliasSet(users, org.AclPolicy, machines)

	return validateAutoApproverAliases(aliases, allowed)
}

func autoApproverRouteData(route string, approvers []string) AutoApproverRoute {
	return AutoApproverRoute{
		Route:     route,
		Approvers: cloneStringSlice(approvers),
	}
}

func removeAutoApproverAlias(aliases []string, target string) ([]string, bool) {
	result := make([]string, 0, len(aliases))
	removed := false
	for _, alias := range aliases {
		if alias == target {
			removed = true
			continue
		}
		result = append(result, alias)
	}

	return result, removed
}

func (h *Mirage) CAPIGetAutoApprovers(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	resData := AutoApproversData{
		Routes:   []AutoApproverRoute{},
		ExitNode: []string{},
	}
	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil {
		h.doAPIResponse(w, "", resData)
		return
	}

	for route, approvers := range org.AclPolicy.AutoApprovers.Routes {
		resData.Routes = append(resData.Routes, autoApproverRouteData(route, approvers))
	}
	sort.Slice(resData.Routes, func(i, j int) bool {
		return resData.Routes[i].Route < resData.Routes[j].Route
	})
	resData.ExitNode = normalizeAutoApproverAliases(org.AclPolicy.AutoApprovers.ExitNode)

	h.doAPIResponse(w, "", resData)
}

func (h *Mirage) CAPIPostAutoApproverRoutes(
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

	reqData := SetAutoApproverRouteREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedRoute, err := normalizeAutoApproverRoute(reqData.Route)
	if err != nil {
		h.doAPIResponse(w, "自动审批路由无效", nil)
		return
	}
	normalizedApprovers := normalizeAutoApproverAliases(reqData.Approvers)
	if err := validateAutoApproverApprovers(normalizedApprovers); err != nil {
		h.doAPIResponse(w, "自动审批审批人无效:"+err.Error(), nil)
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
	if err := h.validateAutoApproverAliasesForOrg(org, normalizedApprovers); err != nil {
		h.doAPIResponse(w, "自动审批审批人无效:"+err.Error(), nil)
		return
	}
	if org.AclPolicy.AutoApprovers.Routes == nil {
		org.AclPolicy.AutoApprovers.Routes = make(map[string][]string)
	}

	candidateRoutes := cloneAutoApproverRoutes(org.AclPolicy.AutoApprovers.Routes)
	switch reqData.State {
	case "create":
		if _, ok := candidateRoutes[normalizedRoute]; ok {
			h.doAPIResponse(w, "occupied", nil)
			return
		}
	case "update":
		previousRoute := reqData.PreviousRoute
		if previousRoute == "" {
			previousRoute = reqData.Route
		}
		normalizedPreviousRoute, err := normalizeAutoApproverRoute(previousRoute)
		if err != nil {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
		if _, ok := candidateRoutes[normalizedPreviousRoute]; !ok {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
		if normalizedPreviousRoute != normalizedRoute {
			if _, ok := candidateRoutes[normalizedRoute]; ok {
				h.doAPIResponse(w, "occupied", nil)
				return
			}
			delete(candidateRoutes, normalizedPreviousRoute)
		}
	default:
		h.doAPIResponse(w, "不支持的操作", nil)
		return
	}

	candidateRoutes[normalizedRoute] = normalizedApprovers
	org.AclPolicy.AutoApprovers.Routes = candidateRoutes
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", autoApproverRouteData(normalizedRoute, normalizedApprovers))
}

func (h *Mirage) CAPIPostAutoApproverExitNode(
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

	reqData := SetAutoApproverExitNodeREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedApprovers := normalizeAutoApproverAliases(reqData.Approvers)
	if len(normalizedApprovers) > 0 {
		if err := validateAutoApproverApprovers(normalizedApprovers); err != nil {
			h.doAPIResponse(w, "出口节点自动审批人无效:"+err.Error(), nil)
			return
		}
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil {
		org.AclPolicy = &ACLPolicy{}
	}
	if err := h.validateAutoApproverAliasesForOrg(org, normalizedApprovers); err != nil {
		h.doAPIResponse(w, "出口节点自动审批人无效:"+err.Error(), nil)
		return
	}

	org.AclPolicy.AutoApprovers.ExitNode = normalizedApprovers
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", cloneStringSlice(normalizedApprovers))
}

func (h *Mirage) CAPIDelAutoApproverRoute(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	rawRoute := strings.TrimPrefix(r.URL.Path, "/admin/api/acls/auto-approvers/routes/")
	decodedRoute, err := parseAutoApproverPathSegment(rawRoute)
	if err != nil {
		h.doAPIResponse(w, "notfound", nil)
		return
	}
	normalizedRoute, err := normalizeAutoApproverRoute(decodedRoute)
	if err != nil {
		h.doAPIResponse(w, "notfound", nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || org.AclPolicy.AutoApprovers.Routes == nil {
		h.doAPIResponse(w, "notfound", nil)
		return
	}
	if _, ok := org.AclPolicy.AutoApprovers.Routes[normalizedRoute]; !ok {
		h.doAPIResponse(w, "notfound", nil)
		return
	}

	delete(org.AclPolicy.AutoApprovers.Routes, normalizedRoute)
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", normalizedRoute)
}

func (h *Mirage) CAPIDelAutoApproverExitNode(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	rawAlias := strings.TrimPrefix(r.URL.Path, "/admin/api/acls/auto-approvers/exit-node/")
	alias, err := parseAutoApproverPathSegment(rawAlias)
	if err != nil {
		h.doAPIResponse(w, "notfound", nil)
		return
	}

	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || len(org.AclPolicy.AutoApprovers.ExitNode) == 0 {
		h.doAPIResponse(w, "notfound", nil)
		return
	}

	nextApprovers, removed := removeAutoApproverAlias(org.AclPolicy.AutoApprovers.ExitNode, alias)
	if !removed {
		h.doAPIResponse(w, "notfound", nil)
		return
	}
	org.AclPolicy.AutoApprovers.ExitNode = normalizeAutoApproverAliases(nextApprovers)
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", alias)
}
