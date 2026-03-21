package controller

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

var aclHostAliasPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type HostsData struct {
	Hosts []ACLHost `json:"hosts"`
}

type ACLHost struct {
	HostName string `json:"hostName"`
	IPPrefix string `json:"ipPrefix"`
}

type SetHostREQ struct {
	State    string `json:"state"`
	HostName string `json:"hostName"`
	IPPrefix string `json:"ipPrefix"`
}

func normalizeACLHostName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func validateACLHostName(name string) bool {
	return aclHostAliasPattern.MatchString(name)
}

func normalizeACLHostPrefix(prefix string) (netip.Prefix, error) {
	prefix = strings.TrimSpace(prefix)
	if addr, err := netip.ParseAddr(prefix); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()).Masked(), nil
	}

	parsed, err := netip.ParsePrefix(prefix)
	if err != nil {
		return netip.Prefix{}, err
	}

	return parsed.Masked(), nil
}

// 接受/admin/api/acls/hosts的Get请求，用于查询hosts
func (h *Mirage) CAPIGetHosts(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	resData := HostsData{Hosts: []ACLHost{}}
	org, err := h.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "用户组织信息获取失败", nil)
		return
	}
	if org.AclPolicy == nil || org.AclPolicy.Hosts == nil {
		h.doAPIResponse(w, "", resData)
		return
	}

	hostNames := make([]string, 0, len(org.AclPolicy.Hosts))
	for hostName := range org.AclPolicy.Hosts {
		hostNames = append(hostNames, hostName)
	}
	sort.Strings(hostNames)

	for _, hostName := range hostNames {
		resData.Hosts = append(resData.Hosts, ACLHost{
			HostName: hostName,
			IPPrefix: org.AclPolicy.Hosts[hostName].String(),
		})
	}

	h.doAPIResponse(w, "", resData)
}

// 接受/admin/api/acls/hosts的Post请求，用于创建或更新hosts
func (h *Mirage) CAPIPostHosts(
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

	reqData := SetHostREQ{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	normalizedHostName := normalizeACLHostName(reqData.HostName)
	if !validateACLHostName(normalizedHostName) {
		h.doAPIResponse(w, "主机别名无效", nil)
		return
	}
	prefix, err := normalizeACLHostPrefix(reqData.IPPrefix)
	if err != nil {
		h.doAPIResponse(w, "IP或网段无效", nil)
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
	if org.AclPolicy.Hosts == nil {
		org.AclPolicy.Hosts = make(Hosts)
	}

	switch reqData.State {
	case "create":
		if _, ok := org.AclPolicy.Hosts[normalizedHostName]; ok {
			h.doAPIResponse(w, "occupied", nil)
			return
		}
	case "update":
		if _, ok := org.AclPolicy.Hosts[normalizedHostName]; !ok {
			h.doAPIResponse(w, "notfound", nil)
			return
		}
	default:
		h.doAPIResponse(w, "不支持的操作", nil)
		return
	}

	org.AclPolicy.Hosts[normalizedHostName] = prefix
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", ACLHost{
		HostName: normalizedHostName,
		IPPrefix: prefix.String(),
	})
}

// 注销Host执行DELETE方法api/acls/hosts/:host
func (h *Mirage) CAPIDelHosts(
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
	if org.AclPolicy == nil || org.AclPolicy.Hosts == nil {
		h.doAPIResponse(w, "该主机别名不存在", nil)
		return
	}

	targetHostName := strings.TrimPrefix(r.URL.Path, "/admin/api/acls/hosts/")
	normalizedHostName := normalizeACLHostName(targetHostName)
	if !validateACLHostName(normalizedHostName) {
		h.doAPIResponse(w, "该主机别名不存在", nil)
		return
	}
	if _, ok := org.AclPolicy.Hosts[normalizedHostName]; !ok {
		h.doAPIResponse(w, "该主机别名不存在", nil)
		return
	}

	delete(org.AclPolicy.Hosts, normalizedHostName)
	if err := h.SaveACLPolicyOfOrg(org); err != nil {
		h.doAPIResponse(w, "保存ACL策略失败:"+err.Error(), nil)
		return
	}
	h.setOrgLastStateChangeToNow(user.OrganizationID)

	h.doAPIResponse(w, "", normalizedHostName)
}
