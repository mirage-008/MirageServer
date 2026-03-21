package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

var aclGroupNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type GroupsData struct {
	Groups []ACLGroup `json:"groups"`
}

type ACLGroup struct {
	GroupName string   `json:"groupName"`
	Users     []string `json:"users"`
}

type SetGroupREQ struct {
	State     string   `json:"state"`
	GroupName string   `json:"groupName"`
	Users     []string `json:"users"`
}

func normalizeACLGroupName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "group:")

	return strings.ToLower(name)
}

func validateACLGroupName(name string) bool {
	return aclGroupNamePattern.MatchString(name)
}

func buildACLGroupName(name string) string {
	return "group:" + normalizeACLGroupName(name)
}

func normalizeACLGroupUsers(users []string) []string {
	result := make([]string, 0, len(users))
	seen := make(map[string]struct{}, len(users))
	for _, user := range users {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		if _, ok := seen[user]; ok {
			continue
		}
		seen[user] = struct{}{}
		result = append(result, user)
	}
	sort.Strings(result)

	return result
}

func (h *Mirage) validateACLGroupUsers(orgID int64, users []string) error {
	if len(users) == 0 {
		return nil
	}

	orgUsers, err := h.ListOrgUsers(orgID)
	if err != nil {
		return err
	}

	allowedUsers := make(map[string]struct{}, len(orgUsers))
	for _, user := range orgUsers {
		allowedUsers[user.Name] = struct{}{}
	}

	invalidUsers := make([]string, 0)
	for _, user := range users {
		if _, ok := allowedUsers[user]; !ok {
			invalidUsers = append(invalidUsers, user)
		}
	}
	if len(invalidUsers) == 0 {
		return nil
	}

	sort.Strings(invalidUsers)

	return fmt.Errorf("以下用户不存在: %s", strings.Join(invalidUsers, ", "))
}

// 接受/admin/api/acls/groups的Get请求，用于查询groups
func (h *Mirage) CAPIGetGroups(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	resData := GroupsData{Groups: []ACLGroup{}}
	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || org.AclPolicy.Groups == nil {
		h.doAPIResponse(w, "", resData)
		return
	}

	groupNames := make([]string, 0, len(org.AclPolicy.Groups))
	for groupName := range org.AclPolicy.Groups {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		groupUsers := append([]string(nil), org.AclPolicy.Groups[groupName]...)
		sort.Strings(groupUsers)
		resData.Groups = append(resData.Groups, ACLGroup{
			GroupName: groupName,
			Users:     groupUsers,
		})
	}

	h.doAPIResponse(w, "", resData)
}

// 接受/admin/api/acls/groups的Post请求，用于创建或更新用户组
func (h *Mirage) CAPIPostGroups(
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

	reqData := SetGroupREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedGroupName := normalizeACLGroupName(reqData.GroupName)
	if !validateACLGroupName(normalizedGroupName) {
		h.doAPIResponse(w, "用户组名称无效", nil)
		return
	}
	normalizedUsers := normalizeACLGroupUsers(reqData.Users)
	if err := h.validateACLGroupUsers(user.OrganizationID, normalizedUsers); err != nil {
		h.doAPIResponse(w, "用户组成员无效:"+err.Error(), nil)
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
	if org.AclPolicy.Groups == nil {
		org.AclPolicy.Groups = make(Groups)
	}

	groupName := buildACLGroupName(normalizedGroupName)
	switch reqData.State {
	case "create":
		if _, ok := org.AclPolicy.Groups[groupName]; ok {
			h.doAPIResponse(w, "occupied", nil)
			return
		}
	case "update":
		if _, ok := org.AclPolicy.Groups[groupName]; !ok {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
	default:
		h.doAPIResponse(w, "不支持的操作", nil)
		return
	}

	org.AclPolicy.Groups[groupName] = normalizedUsers
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", ACLGroup{
		GroupName: groupName,
		Users:     normalizedUsers,
	})
}

// 注销Group执行DELETE方法api/acls/groups/:group
func (h *Mirage) CAPIDelGroups(
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
	if org.AclPolicy == nil || org.AclPolicy.Groups == nil {
		h.doAPIResponse(w, "该用户组不存在", nil)
		return
	}

	targetGroupName := strings.TrimPrefix(r.URL.Path, "/admin/api/acls/groups/")
	normalizedGroupName := normalizeACLGroupName(targetGroupName)
	if !validateACLGroupName(normalizedGroupName) {
		h.doAPIResponse(w, "该用户组不存在", nil)
		return
	}

	groupName := buildACLGroupName(normalizedGroupName)
	if _, ok := org.AclPolicy.Groups[groupName]; !ok {
		h.doAPIResponse(w, "该用户组不存在", nil)
		return
	}

	delete(org.AclPolicy.Groups, groupName)
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", normalizedGroupName)
}
