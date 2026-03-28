package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tailscale.com/tailcfg"
)

type UsersData struct {
	Users          []UserData          `json:"users"`
	ExternalUsers  []UserData          `json:"externalUsers"`
	PendingInvites []PendingInviteData `json:"pendingInvites"`
	CurrentUserID  int64               `json:"currentUserID"`
	OwnerID        int64               `json:"ownerID"`
	DomainHasOwner bool                `json:"domainHasOwner"`
}

type UserData struct {
	Id                 string    `json:"id"`
	StableId           string    `json:"stableId"`
	DisplayName        string    `json:"displayName"`
	LoginName          string    `json:"loginName"`
	DomainName         string    `json:"domainName"`
	SharedDomain       bool      `json:"sharedDomain"`
	ProfilePicURL      string    `json:"profilePicURL"`
	Created            time.Time `json:"created"`
	Role               string    `json:"role"`
	IsAdmin            bool      `json:"isAdmin"`
	IsOwner            bool      `json:"isOwner"`
	Status             string    `json:"status"`
	DeviceCount        int       `json:"deviceCount"`
	CanEditBilling     bool      `json:"canEditBilling"`
	NeedsOnboarding    bool      `json:"needsOnboarding"`
	LastSeen           time.Time `json:"lastSeen"`
	CurrentlyConnected bool      `json:"currentlyConnected"`
}

type PendingInviteData struct {
	Id             string     `json:"id"`
	StableId       string     `json:"stableId"`
	TargetIdentity string     `json:"targetIdentity"`
	Created        time.Time  `json:"created"`
	Status         string     `json:"status"`
	InviteToken    string     `json:"inviteToken"`
	InviteURL      string     `json:"inviteURL"`
	AcceptedAt     *time.Time `json:"acceptedAt"`
	RejectedAt     *time.Time `json:"rejectedAt"`
	RevokedAt      *time.Time `json:"revokedAt"`
}

// 请求报文：
type UserActionREQ struct {
	UserID string `json:"userID"`
	Action string `json:"action"` // "restore_user", "suspend_user", "delete_user", "set_owner", "set_member"
}

func buildConsoleUserData(h *Mirage, u User, sharedDomain bool) UserData {
	devCount := 0
	lastSeen := u.CreatedAt
	currentlyConnected := false

	userMachines, err := h.ListMachinesByUser(u.ID)
	if err == nil {
		devCount = len(userMachines)
		for _, m := range userMachines {
			if m.LastSeen != nil && m.LastSeen.After(lastSeen) {
				lastSeen = *m.LastSeen
			}
			if !currentlyConnected && m.isOnline() {
				currentlyConnected = true
			}
		}
	}

	return UserData{
		Id:                 strconv.FormatInt(u.ID, 10),
		StableId:           u.StableID,
		DisplayName:        u.Display_Name,
		LoginName:          u.Name,
		DomainName:         u.Organization.Name,
		SharedDomain:       sharedDomain,
		ProfilePicURL:      "",
		Created:            u.CreatedAt.UTC(),
		Role:               RoleStr[u.Role],
		IsAdmin:            u.Role == RoleOwner,
		IsOwner:            u.Role == RoleOwner,
		Status:             "active",
		DeviceCount:        devCount,
		CanEditBilling:     u.Role == RoleOwner,
		NeedsOnboarding:    false,
		LastSeen:           lastSeen.UTC().Round(time.Second),
		CurrentlyConnected: currentlyConnected,
	}
}

func buildPendingInviteData(h *Mirage, invites []OrgInvite) []PendingInviteData {
	items := make([]PendingInviteData, 0, len(invites))
	for _, invite := range invites {
		items = append(items, PendingInviteData{
			Id:             strconv.FormatInt(invite.ID, 10),
			StableId:       invite.StableID,
			TargetIdentity: invite.TargetIdentity,
			Created:        invite.CreatedAt.UTC(),
			Status:         invite.Status,
			InviteToken:    invite.InviteToken,
			InviteURL:      h.buildOrgInviteURL(invite.InviteToken),
			AcceptedAt:     invite.AcceptedAt,
			RejectedAt:     invite.RejectedAt,
			RevokedAt:      invite.RevokedAt,
		})
	}
	return items
}

func (h *Mirage) buildUsersData(user *User) (*UsersData, error) {
	resData := &UsersData{
		Users:          []UserData{},
		ExternalUsers:  []UserData{},
		PendingInvites: []PendingInviteData{},
		CurrentUserID:  user.ID,
		OwnerID:        user.ID,
		DomainHasOwner: true,
	}

	users, err := h.ListOrgUsers(user.OrganizationID)
	if err != nil {
		return nil, err
	}
	for _, orgUser := range users {
		resData.Users = append(resData.Users, buildConsoleUserData(h, orgUser, false))
		if orgUser.Role == RoleOwner {
			resData.OwnerID = orgUser.ID
		}
	}

	externalUsers, err := h.ListExternalSharedUsersByTargetUserID(user.ID)
	if err != nil {
		return nil, err
	}
	for _, externalUser := range externalUsers {
		resData.ExternalUsers = append(resData.ExternalUsers, buildConsoleUserData(h, externalUser, true))
	}

	invites, err := h.ListOrgInvitesByOrgID(user.OrganizationID)
	if err != nil {
		return nil, err
	}
	resData.PendingInvites = buildPendingInviteData(h, invites)

	return resData, nil
}

func parseRequestString(reqData map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := reqData[key].(string)
		if ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func parseRequestInt64(reqData map[string]interface{}, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, ok := reqData[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return parsed, true
			}
		case float64:
			return int64(v), true
		}
	}
	return 0, false
}

func mapInviteErrorMessage(err error) string {
	switch err {
	case ErrOrgInviteTargetInvalid:
		return "目标身份无效"
	case ErrOrgInviteTargetAlreadyInOrg:
		return "目标身份已在当前组织中"
	case ErrOrgInviteTargetHasPendingInvite:
		return "目标身份已有待处理邀请"
	case ErrOrgInviteTargetBelongsToOtherOrg:
		return "目标身份已属于其他组织"
	case ErrOrgInviteTargetMismatch:
		return "登录身份与邀请目标不匹配"
	case ErrOrgInviteNotFound:
		return "邀请不存在"
	case ErrOrgInviteAlreadyAccepted:
		return "邀请已被接受"
	case ErrOrgInviteAlreadyRejected:
		return "邀请已被拒绝"
	case ErrOrgInviteAlreadyRevoked:
		return "邀请已被撤销"
	default:
		return err.Error()
	}
}

// 接受/admin/api/users的Get请求，用于查询用户
func (h *Mirage) CAPIGetUsers(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return
	}

	resData, err := h.buildUsersData(user)
	if err != nil {
		h.doAPIResponse(w, "用户列表获取失败:"+err.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", resData)
}

// 接受/admin/api/users的Post请求，用于对用户操作
func (h *Mirage) CAPIPostUsers(
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

	reqData := map[string]interface{}{}
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) && err.Error() != "EOF" {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	action := strings.TrimSpace(strings.ToLower(parseRequestString(reqData, "action")))
	if action == "" {
		var legacyReq UserActionREQ
		if rawAction, ok := reqData["action"].(string); ok {
			legacyReq.Action = rawAction
		}
		if rawUserID, ok := reqData["userID"].(string); ok {
			legacyReq.UserID = rawUserID
		}
		action = strings.TrimSpace(strings.ToLower(legacyReq.Action))
	}

	switch action {
	case "create_invite", "create-invite":
		if user.Role != RoleOwner {
			h.doAPIResponse(w, "权限不足", nil)
			return
		}
		targetIdentity := parseRequestString(reqData, "targetIdentity", "targetUser", "targetUserName", "targetLoginName")
		invite, err := h.CreateOrgInvite(user.OrganizationID, user.ID, targetIdentity)
		if err != nil {
			h.doAPIResponse(w, "创建邀请失败:"+mapInviteErrorMessage(err), nil)
			return
		}
		h.doAPIResponse(w, "", map[string]interface{}{
			"invite": map[string]interface{}{
				"id":             strconv.FormatInt(invite.ID, 10),
				"stableId":       invite.StableID,
				"targetIdentity": invite.TargetIdentity,
				"status":         invite.Status,
				"created":        invite.CreatedAt.UTC(),
				"inviteToken":    invite.InviteToken,
				"inviteURL":      h.buildOrgInviteURL(invite.InviteToken),
				"acceptedAt":     invite.AcceptedAt,
				"rejectedAt":     invite.RejectedAt,
				"revokedAt":      invite.RevokedAt,
			},
		})
		return

	case "revoke_invite", "revoke-invite":
		if user.Role != RoleOwner {
			h.doAPIResponse(w, "权限不足", nil)
			return
		}
		inviteID, ok := parseRequestInt64(reqData, "inviteID", "inviteId")
		if !ok {
			h.doAPIResponse(w, "目标邀请ID解析失败", nil)
			return
		}
		if err := h.RevokeOrgInvite(inviteID, user.OrganizationID); err != nil {
			h.doAPIResponse(w, "撤销邀请失败:"+mapInviteErrorMessage(err), nil)
			return
		}
		h.doAPIResponse(w, "", nil)
		return

	case "set_owner":
		if user.Role != RoleOwner {
			h.doAPIResponse(w, "权限不足", nil)
			return
		}
		targetUID, ok := parseRequestInt64(reqData, "userID", "userId")
		if !ok {
			h.doAPIResponse(w, "目标用户ID解析失败", nil)
			return
		}
		if err := h.TransferOwner(tailcfg.UserID(user.ID), tailcfg.UserID(targetUID)); err != nil {
			h.doAPIResponse(w, "修改用户角色失败:"+err.Error(), nil)
			return
		}
		h.doAPIResponse(w, "", nil)
		return

	case "delete_user":
		targetUID, ok := parseRequestInt64(reqData, "userID", "userId")
		if !ok {
			h.doAPIResponse(w, "目标用户ID解析失败", nil)
			return
		}
		targetUser, err := h.GetUserByID(tailcfg.UserID(targetUID))
		if err != nil {
			h.doAPIResponse(w, "目标用户信息获取失败:"+err.Error(), nil)
			return
		}
		if targetUser.Role == RoleOwner {
			h.doAPIResponse(w, "无法删除Owner，请联系我们", nil)
			return
		}

		mlist, err := h.ListMachinesByUser(targetUID)
		if err != nil {
			h.doAPIResponse(w, "目标用户设备列表获取失败:"+err.Error(), nil)
			return
		}
		for _, m := range mlist {
			if m.ForcedTags != nil && len([]string(m.ForcedTags)) > 0 {
				continue
			}
			if err = h.HardDeleteMachine(&m); err != nil {
				h.doAPIResponse(w, "目标用户设备删除失败:"+err.Error(), nil)
				return
			}
			h.NotifyNaviOrgNodesChange(user.OrganizationID, "", m.NodeKey)
		}
		if err = h.DestroyUser(targetUser.Name, targetUser.Organization.Name, targetUser.Organization.Provider); err != nil {
			h.doAPIResponse(w, "目标用户删除失败:"+err.Error(), nil)
			return
		}
		h.doAPIResponse(w, "", nil)
		return
	}

	h.doAPIResponse(w, "未知操作", nil)
}
