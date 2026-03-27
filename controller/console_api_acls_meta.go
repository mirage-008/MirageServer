package controller

import "net/http"

type ACLMetaData struct {
	Sections         []string `json:"sections"`
	SSHEnabled       bool     `json:"sshEnabled"`
	TestsImplemented bool     `json:"testsImplemented"`
}

func (h *Mirage) CAPIGetACLMeta(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	sections := []string{"policy", "rules", "auto-approvers", "tags", "groups", "hosts"}
	if featureEnableSSH() {
		sections = append(sections, "ssh")
	}

	h.doAPIResponse(w, "", ACLMetaData{
		Sections:         sections,
		SSHEnabled:       featureEnableSSH(),
		TestsImplemented: false,
	})
}
