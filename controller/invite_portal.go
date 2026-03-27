package controller

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/patrickmn/go-cache"
	"tailscale.com/tailcfg"
)

const inviteAuthCookieName = "mirageinvite"

type inviteAuthSession struct {
	UserName    string
	UserDisName string
	Provider    string
}

type invitePortalView struct {
	Title               string
	Heading             string
	Description         string
	Inviter             string
	TargetIdentity      string
	StatusLabel         string
	StatusClass         string
	StatusDetail        string
	ErrorMessage        string
	LoginURL            string
	RegisterURL         string
	LogoutURL           string
	ShowLogin           bool
	ShowRegister        bool
	ShowLogout          bool
	ShowActions         bool
	ShowCurrentIdentity bool
	CurrentIdentity     string
	CurrentOrg          string
}

func (h *Mirage) ensureInviteAuthCache() *cache.Cache {
	if h.inviteAuthCache == nil {
		h.inviteAuthCache = cache.New(0, 0)
	}

	return h.inviteAuthCache
}

func (h *Mirage) setInviteAuthSession(w http.ResponseWriter, session inviteAuthSession) {
	cacheKey := h.GenStateCode()
	h.ensureInviteAuthCache().Set(cacheKey, session, time.Until(time.Now().AddDate(0, 0, 7)))

	http.SetCookie(w, &http.Cookie{
		Name:     inviteAuthCookieName,
		Value:    cacheKey,
		Domain:   h.cfg.ServerURL,
		Path:     "/",
		Expires:  time.Now().AddDate(0, 0, 7),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Mirage) getInviteAuthSession(r *http.Request) (*inviteAuthSession, bool) {
	if h == nil || r == nil {
		return nil, false
	}

	cookie, err := r.Cookie(inviteAuthCookieName)
	if err != nil {
		return nil, false
	}

	raw, _, ok := h.ensureInviteAuthCache().GetWithExpiration(cookie.Value)
	if !ok {
		return nil, false
	}

	session, ok := raw.(inviteAuthSession)
	if !ok {
		return nil, false
	}

	return &session, true
}

func (h *Mirage) clearInviteAuthSession(w http.ResponseWriter, r *http.Request) {
	if r != nil {
		if cookie, err := r.Cookie(inviteAuthCookieName); err == nil {
			h.ensureInviteAuthCache().Delete(cookie.Value)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     inviteAuthCookieName,
		Domain:   h.cfg.ServerURL,
		Expires:  time.Now().Add(-time.Hour),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		Path:     "/",
	})
}

func (h *Mirage) setControlCodeCookieForUser(w http.ResponseWriter, userID tailcfg.UserID) {
	if h.controlCodeCache == nil {
		h.controlCodeCache = cache.New(0, 0)
	}

	controlCode := h.GenStateCode()
	h.controlCodeCache.Set(
		controlCode,
		ControlCacheItem{uid: userID},
		time.Until(time.Now().AddDate(0, 1, 0)),
	)

	http.SetCookie(w, &http.Cookie{
		Name:     "miragecontrol",
		Value:    controlCode,
		Domain:   h.cfg.ServerURL,
		Path:     "/",
		Expires:  time.Now().AddDate(0, 1, 0),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func invitePageLoginURL(nextPath string, openRegister bool) string {
	values := url.Values{}
	values.Set("next_url", nextPath)
	if openRegister {
		values.Set("register", "1")
	}

	return "/login?" + values.Encode()
}

func invitePageLogoutURL(nextPath string) string {
	values := url.Values{}
	values.Set("next_url", nextPath)
	return "/logout?" + values.Encode()
}

func orgInviteTokenFromNextURL(nextURL string) string {
	parsed, err := url.Parse(nextURL)
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "invite" && parts[1] == "org" {
		return strings.TrimSpace(parts[2])
	}

	return ""
}

func inviteStatusView(status string) (string, string) {
	switch status {
	case "accepted":
		return "已接受", "status-accepted"
	case "rejected":
		return "已拒绝", "status-rejected"
	case "revoked":
		return "已撤销", "status-revoked"
	default:
		return "待处理", "status-pending"
	}
}

func inviteDecisionTime(acceptedAt, rejectedAt, revokedAt *time.Time) string {
	switch {
	case acceptedAt != nil:
		return "处理时间：已于 " + acceptedAt.UTC().Format("2006-01-02 15:04:05 UTC") + " 接受"
	case rejectedAt != nil:
		return "处理时间：已于 " + rejectedAt.UTC().Format("2006-01-02 15:04:05 UTC") + " 拒绝"
	case revokedAt != nil:
		return "处理时间：已于 " + revokedAt.UTC().Format("2006-01-02 15:04:05 UTC") + " 撤销"
	default:
		return ""
	}
}

func resolveInviteViewer(h *Mirage, w http.ResponseWriter, r *http.Request) (*User, *inviteAuthSession) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err == nil && user != nil && !user.CheckEmpty() {
		return user, nil
	}

	session, ok := h.getInviteAuthSession(r)
	if !ok {
		return nil, nil
	}

	return nil, session
}

func userDisplayName(user *User) string {
	if user == nil {
		return ""
	}
	if strings.TrimSpace(user.Display_Name) != "" {
		return user.Display_Name
	}
	return user.Name
}

func (h *Mirage) renderInvitePortal(w http.ResponseWriter, view invitePortalView) {
	tmpl := template.Must(template.New("invite-portal").Parse(invitePortalHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, view)
}

func (h *Mirage) OrgInvitePortal(w http.ResponseWriter, r *http.Request) {
	inviteToken := mux.Vars(r)["inviteToken"]
	currentPath := "/invite/org/" + inviteToken
	actionErrorMessage := ""

	invite, err := h.GetOrgInviteByToken(inviteToken)
	if err != nil {
		h.renderInvitePortal(w, invitePortalView{
			Title:        "组织邀请",
			Heading:      "邀请无效",
			Description:  "该组织邀请不存在，或已经不可用。",
			StatusLabel:  "邀请无效",
			StatusClass:  "status-revoked",
			ShowLogin:    false,
			ShowActions:  false,
			ErrorMessage: "",
		})
		return
	}

	user, authSession := resolveInviteViewer(h, w, r)
	currentIdentity := ""
	currentOrg := ""
	currentName := ""
	if user != nil {
		currentIdentity = user.Name
		currentOrg = user.Organization.Name
		currentName = userDisplayName(user)
	} else if authSession != nil {
		currentIdentity = authSession.UserName
		currentName = authSession.UserDisName
	}

	if r.Method == http.MethodPost {
		action := strings.TrimSpace(r.FormValue("action"))
		switch action {
		case "accept":
			var (
				acceptedInvite *OrgInvite
				invitedUser    *User
				actionErr      error
			)
			switch {
			case authSession != nil:
				acceptedInvite, invitedUser, actionErr = h.AcceptOrgInviteByToken(inviteToken, authSession.UserName, authSession.UserDisName)
			case user != nil:
				acceptedInvite, invitedUser, actionErr = h.AcceptOrgInviteByToken(inviteToken, user.Name, userDisplayName(user))
			default:
				http.Redirect(w, r, currentPath, http.StatusFound)
				return
			}
			if actionErr == nil && invitedUser != nil {
				h.clearInviteAuthSession(w, r)
				h.setControlCodeCookieForUser(w, tailcfg.UserID(invitedUser.ID))
				http.Redirect(w, r, currentPath, http.StatusFound)
				return
			}
			if actionErr == nil && acceptedInvite != nil {
				http.Redirect(w, r, currentPath, http.StatusFound)
				return
			}
			actionErrorMessage = mapInviteErrorMessage(actionErr)
		case "reject":
			identity := currentIdentity
			if identity == "" && authSession != nil {
				identity = authSession.UserName
			}
			if identity != "" {
				if _, rejectErr := h.RejectOrgInviteByToken(inviteToken, identity); rejectErr == nil {
					h.clearInviteAuthSession(w, r)
					http.Redirect(w, r, currentPath, http.StatusFound)
					return
				} else {
					actionErrorMessage = mapInviteErrorMessage(rejectErr)
				}
			} else {
				actionErrorMessage = "请先登录后再处理邀请"
			}
		}
	}

	statusLabel, statusClass := inviteStatusView(invite.Status)
	showActions := invite.Status == OrgInviteStatusPending && currentIdentity != "" && normalizeExternalIdentity(currentIdentity) == invite.TargetIdentity
	showLogin := invite.Status == OrgInviteStatusPending && currentIdentity == ""

	description := "接受后，该身份将加入组织 “" + invite.Org.Name + "”。"
	if showActions {
		description = "当前身份已验证，可在这里明确接受或拒绝组织邀请。"
	} else if invite.Status == OrgInviteStatusPending && currentIdentity != "" && normalizeExternalIdentity(currentIdentity) != invite.TargetIdentity {
		description = "当前登录身份与邀请目标不匹配，请切换到受邀身份后再处理。"
		actionErrorMessage = "当前登录身份与邀请目标不匹配"
	}

	view := invitePortalView{
		Title:               "组织邀请",
		Heading:             "加入组织邀请",
		Description:         description,
		Inviter:             invite.Org.Name,
		TargetIdentity:      invite.TargetIdentity,
		StatusLabel:         statusLabel,
		StatusClass:         statusClass,
		StatusDetail:        inviteDecisionTime(invite.AcceptedAt, invite.RejectedAt, invite.RevokedAt),
		ErrorMessage:        actionErrorMessage,
		LoginURL:            invitePageLoginURL(currentPath, false),
		RegisterURL:         invitePageLoginURL(currentPath, true),
		LogoutURL:           invitePageLogoutURL(currentPath),
		ShowLogin:           showLogin,
		ShowRegister:        showLogin && h.cfg.SelfRegistrationEnabled(),
		ShowLogout:          currentIdentity != "",
		ShowActions:         showActions,
		ShowCurrentIdentity: currentIdentity != "",
		CurrentIdentity:     currentName,
		CurrentOrg:          currentOrg,
	}
	h.renderInvitePortal(w, view)
}

func (h *Mirage) DeviceSharePortal(w http.ResponseWriter, r *http.Request) {
	shareToken := mux.Vars(r)["shareToken"]
	currentPath := "/invite/device/" + shareToken
	actionErrorMessage := ""

	share := &MachineShare{}
	if err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").First(share, "share_token = ?", strings.TrimSpace(shareToken)).Error; err != nil {
		h.renderInvitePortal(w, invitePortalView{
			Title:       "设备分享",
			Heading:     "分享无效",
			Description: "该设备分享不存在，或已经不可用。",
			StatusLabel: "分享无效",
			StatusClass: "status-revoked",
		})
		return
	}

	user, _ := resolveInviteViewer(h, w, r)
	currentIdentity := ""
	currentOrg := ""
	currentName := ""
	if user != nil {
		currentIdentity = user.Name
		currentOrg = user.Organization.Name
		currentName = userDisplayName(user)
	}

	if r.Method == http.MethodPost && user != nil {
		action := strings.TrimSpace(r.FormValue("action"))
		switch action {
		case "accept":
			if _, err := h.AcceptMachineShareByToken(shareToken, user); err == nil {
				http.Redirect(w, r, currentPath, http.StatusFound)
				return
			} else {
				actionErrorMessage = mapShareErrorMessage(err, false)
			}
		case "reject":
			if _, err := h.RejectMachineShareByToken(shareToken, user); err == nil {
				http.Redirect(w, r, currentPath, http.StatusFound)
				return
			} else {
				actionErrorMessage = mapShareErrorMessage(err, false)
			}
		}
	} else if r.Method == http.MethodPost {
		actionErrorMessage = "请先登录后再处理分享"
	}

	statusLabel, statusClass := inviteStatusView(share.Status)
	showActions := share.Status == MachineShareStatusPending && user != nil && normalizeExternalIdentity(currentIdentity) == share.TargetIdentity
	showLogin := share.Status == MachineShareStatusPending && user == nil

	description := "接受后，当前登录组织将可见设备 “" + share.SourceMachine.GivenName + "”。"
	if showActions {
		description = "当前登录身份已匹配，可在这里明确接受或拒绝设备分享。"
	} else if share.Status == MachineShareStatusPending && user != nil && normalizeExternalIdentity(currentIdentity) != share.TargetIdentity {
		description = "当前登录身份与分享目标不匹配，请切换到受邀身份后再处理。"
		actionErrorMessage = "当前登录身份与分享目标不匹配"
	}

	view := invitePortalView{
		Title:               "设备分享",
		Heading:             "设备分享邀请",
		Description:         description,
		Inviter:             userDisplayName(&share.SourceMachine.User),
		TargetIdentity:      share.TargetIdentity,
		StatusLabel:         statusLabel,
		StatusClass:         statusClass,
		StatusDetail:        inviteDecisionTime(share.AcceptedAt, share.RejectedAt, share.RevokedAt),
		ErrorMessage:        actionErrorMessage,
		LoginURL:            invitePageLoginURL(currentPath, false),
		RegisterURL:         invitePageLoginURL(currentPath, true),
		LogoutURL:           invitePageLogoutURL(currentPath),
		ShowLogin:           showLogin,
		ShowRegister:        showLogin && h.cfg.SelfRegistrationEnabled(),
		ShowLogout:          currentIdentity != "",
		ShowActions:         showActions,
		ShowCurrentIdentity: currentIdentity != "",
		CurrentIdentity:     currentName,
		CurrentOrg:          currentOrg,
	}
	h.renderInvitePortal(w, view)
}

func (h *Mirage) maybeBridgeInviteLogin(w http.ResponseWriter, stateItem StateCacheItem) bool {
	inviteToken := orgInviteTokenFromNextURL(stateItem.nextURL)
	if inviteToken == "" {
		return false
	}

	invite, err := h.GetOrgInviteByToken(inviteToken)
	if err != nil || invite.Status != OrgInviteStatusPending {
		return false
	}
	if invite.TargetIdentity != normalizeExternalIdentity(stateItem.userName) {
		return false
	}

	existingUsers, err := h.ListUsersByNormalizedName(stateItem.userName)
	if err != nil || len(existingUsers) != 0 {
		return false
	}

	h.setInviteAuthSession(w, inviteAuthSession{
		UserName:    stateItem.userName,
		UserDisName: stateItem.userDisName,
		Provider:    stateItem.provider,
	})
	return true
}

const invitePortalHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ .Title }}</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: linear-gradient(180deg, #f8fafc 0%, #eef2ff 100%); color: #0f172a; }
    .wrap { min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 24px; }
    .card { width: 100%; max-width: 720px; background: #fff; border: 1px solid #e2e8f0; border-radius: 24px; box-shadow: 0 24px 80px rgba(15, 23, 42, 0.08); overflow: hidden; }
    .head { padding: 32px 32px 20px; border-bottom: 1px solid #eef2f7; }
    .body { padding: 24px 32px 32px; }
    h1 { margin: 0 0 12px; font-size: 30px; line-height: 1.2; }
    p { margin: 0; line-height: 1.6; color: #475569; }
    .status { display: inline-flex; align-items: center; padding: 8px 14px; border-radius: 999px; font-size: 14px; font-weight: 600; margin-bottom: 16px; }
    .status-pending { background: #eff6ff; color: #1d4ed8; }
    .status-accepted { background: #ecfdf5; color: #047857; }
    .status-rejected { background: #fff7ed; color: #c2410c; }
    .status-revoked { background: #f8fafc; color: #475569; }
    .meta { display: grid; gap: 14px; margin: 24px 0; padding: 20px; background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 18px; }
    .meta b { display: block; font-size: 13px; color: #64748b; margin-bottom: 4px; }
    .actions { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 24px; }
    .btn { display: inline-flex; align-items: center; justify-content: center; min-height: 44px; padding: 0 18px; border-radius: 12px; border: 1px solid #cbd5e1; background: #fff; color: #0f172a; text-decoration: none; font-weight: 600; cursor: pointer; }
    .btn-primary { background: #0f172a; border-color: #0f172a; color: #fff; }
    .btn-danger { background: #fff5f5; border-color: #fecaca; color: #b91c1c; }
    .hint { margin-top: 18px; font-size: 14px; color: #64748b; }
    form { margin: 0; }
    @media (max-width: 640px) { .head, .body { padding-left: 20px; padding-right: 20px; } h1 { font-size: 24px; } }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="card">
      <div class="head">
        <div class="status {{ .StatusClass }}">{{ .StatusLabel }}</div>
        <h1>{{ .Heading }}</h1>
        <p>{{ .Description }}</p>
      </div>
      <div class="body">
        <div class="meta">
          <div><b>发起方</b><span>{{ .Inviter }}</span></div>
          <div><b>目标身份</b><span>{{ .TargetIdentity }}</span></div>
          {{ if .ShowCurrentIdentity }}<div><b>当前身份</b><span>{{ .CurrentIdentity }}{{ if .CurrentOrg }} · {{ .CurrentOrg }}{{ end }}</span></div>{{ end }}
          {{ if .StatusDetail }}<div><b>状态说明</b><span>{{ .StatusDetail }}</span></div>{{ end }}
          {{ if .ErrorMessage }}<div><b>提示</b><span>{{ .ErrorMessage }}</span></div>{{ end }}
        </div>

        {{ if .ShowActions }}
        <div class="actions">
          <form method="post"><input type="hidden" name="action" value="accept"><button class="btn btn-primary" type="submit">接受</button></form>
          <form method="post"><input type="hidden" name="action" value="reject"><button class="btn btn-danger" type="submit">拒绝</button></form>
        </div>
        {{ end }}

        {{ if .ShowLogin }}
        <div class="actions">
          <a class="btn btn-primary" href="{{ .LoginURL }}">登录后处理</a>
          {{ if .ShowRegister }}<a class="btn" href="{{ .RegisterURL }}">注册账号</a>{{ end }}
        </div>
        <div class="hint">登录后会回到当前邀请页，可再决定接受还是拒绝。</div>
        {{ end }}

        {{ if .ShowLogout }}
        <div class="actions">
          <a class="btn" href="{{ .LogoutURL }}">切换身份</a>
        </div>
        {{ end }}
      </div>
    </div>
  </div>
</body>
</html>`
