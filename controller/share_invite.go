package controller

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
)

const (
	MachineShareStatusPending  = "pending"
	MachineShareStatusAccepted = "accepted"
	MachineShareStatusRejected = "rejected"
	MachineShareStatusRevoked  = "revoked"

	OrgInviteStatusPending  = "pending"
	OrgInviteStatusAccepted = "accepted"
	OrgInviteStatusRejected = "rejected"
	OrgInviteStatusRevoked  = "revoked"
)

const (
	ErrMachineShareNotFound             = Error("machine share not found")
	ErrMachineShareTargetInvalid        = Error("machine share target is invalid")
	ErrMachineShareTargetMismatch       = Error("machine share target identity does not match current user")
	ErrMachineShareAlreadyAccepted      = Error("machine share has already been accepted")
	ErrMachineShareAlreadyRejected      = Error("machine share has already been rejected")
	ErrMachineShareAlreadyRevoked       = Error("machine share has been revoked")
	ErrMachineShareTargetAlreadyInOrg   = Error("machine share target already belongs to this organization")
	ErrOrgInviteNotFound                = Error("organization invite not found")
	ErrOrgInviteTargetInvalid           = Error("organization invite target is invalid")
	ErrOrgInviteTargetMismatch          = Error("organization invite target identity does not match current user")
	ErrOrgInviteAlreadyAccepted         = Error("organization invite has already been accepted")
	ErrOrgInviteAlreadyRejected         = Error("organization invite has already been rejected")
	ErrOrgInviteAlreadyRevoked          = Error("organization invite has been revoked")
	ErrOrgInviteTargetAlreadyInOrg      = Error("organization invite target already belongs to this organization")
	ErrOrgInviteTargetHasPendingInvite  = Error("organization invite target already has a pending invite")
	ErrOrgInviteTargetBelongsToOtherOrg = Error("organization invite target already belongs to another organization")
)

type MachineShare struct {
	ID              int64   `gorm:"primary_key;unique;not null"`
	StableID        string  `gorm:"unique"`
	SourceMachineID int64   `gorm:"index"`
	SourceMachine   Machine `gorm:"foreignKey:SourceMachineID"`
	SourceOrgID     int64   `gorm:"index"`
	SourceUserID    int64   `gorm:"index"`
	TargetIdentity  string  `gorm:"index"`
	TargetUserID    int64   `gorm:"index"`
	TargetOrgID     int64   `gorm:"index"`
	Status          string  `gorm:"index"`
	ShareToken      string  `gorm:"uniqueIndex"`
	AcceptedAt      *time.Time
	RejectedAt      *time.Time
	RevokedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (share *MachineShare) BeforeCreate(tx *gorm.DB) error {
	if share.ID == 0 {
		flakeID, err := snowflake.NewNode(1)
		if err != nil {
			return err
		}
		share.ID = flakeID.Generate().Int64()
	}
	share.StableID = GetShortId(share.ID)
	share.TargetIdentity = normalizeExternalIdentity(share.TargetIdentity)
	if share.Status == "" {
		share.Status = MachineShareStatusPending
	}
	if share.ShareToken == "" {
		token, err := GenerateRandomStringURLSafe(24)
		if err != nil {
			return err
		}
		share.ShareToken = token
	}

	return nil
}

type OrgInvite struct {
	ID             int64        `gorm:"primary_key;unique;not null"`
	StableID       string       `gorm:"unique"`
	OrgID          int64        `gorm:"index"`
	Org            Organization `gorm:"foreignKey:OrgID"`
	InviterUserID  int64        `gorm:"index"`
	TargetIdentity string       `gorm:"index"`
	AcceptedUserID int64        `gorm:"index"`
	Status         string       `gorm:"index"`
	InviteToken    string       `gorm:"uniqueIndex"`
	AcceptedAt     *time.Time
	RejectedAt     *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (invite *OrgInvite) BeforeCreate(tx *gorm.DB) error {
	if invite.ID == 0 {
		flakeID, err := snowflake.NewNode(1)
		if err != nil {
			return err
		}
		invite.ID = flakeID.Generate().Int64()
	}
	invite.StableID = GetShortId(invite.ID)
	invite.TargetIdentity = normalizeExternalIdentity(invite.TargetIdentity)
	if invite.Status == "" {
		invite.Status = OrgInviteStatusPending
	}
	if invite.InviteToken == "" {
		token, err := GenerateRandomStringURLSafe(24)
		if err != nil {
			return err
		}
		invite.InviteToken = token
	}

	return nil
}

func normalizeExternalIdentity(identity string) string {
	return strings.ToLower(strings.TrimSpace(identity))
}

func normalizePublicBaseURL(raw string) string {
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		return ""
	}
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		return strings.TrimRight(baseURL, "/")
	}

	return "https://" + strings.TrimRight(baseURL, "/")
}

func (h *Mirage) buildPublicURL(path string) string {
	if h == nil || h.cfg == nil {
		return path
	}

	baseURL := normalizePublicBaseURL(h.cfg.ServerURL)
	if baseURL == "" {
		return path
	}

	return baseURL + path
}

func (h *Mirage) buildOrgInviteURL(inviteToken string) string {
	return h.buildPublicURL("/invite/org/" + strings.TrimSpace(inviteToken))
}

func (h *Mirage) buildMachineShareURL(shareToken string) string {
	return h.buildPublicURL("/invite/device/" + strings.TrimSpace(shareToken))
}

func orgInviteStatusError(status string) error {
	switch status {
	case OrgInviteStatusAccepted:
		return ErrOrgInviteAlreadyAccepted
	case OrgInviteStatusRejected:
		return ErrOrgInviteAlreadyRejected
	case OrgInviteStatusRevoked:
		return ErrOrgInviteAlreadyRevoked
	default:
		return ErrOrgInviteNotFound
	}
}

func machineShareStatusError(status string) error {
	switch status {
	case MachineShareStatusAccepted:
		return ErrMachineShareAlreadyAccepted
	case MachineShareStatusRejected:
		return ErrMachineShareAlreadyRejected
	case MachineShareStatusRevoked:
		return ErrMachineShareAlreadyRevoked
	default:
		return ErrMachineShareNotFound
	}
}

func mergeMachines(machineSets ...[]Machine) []Machine {
	merged := make(map[int64]Machine)
	for _, machines := range machineSets {
		for _, machine := range machines {
			if existing, ok := merged[machine.ID]; ok {
				machine.Shared = machine.Shared || existing.Shared
			}
			merged[machine.ID] = machine
		}
	}

	result := make([]Machine, 0, len(merged))
	for _, machine := range merged {
		result = append(result, machine)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	return result
}

func (h *Mirage) listMachinesByIDs(machineIDs []int64) ([]Machine, error) {
	if len(machineIDs) == 0 {
		return []Machine{}, nil
	}
	seen := make(map[int64]struct{}, len(machineIDs))
	uniqueIDs := make([]int64, 0, len(machineIDs))
	for _, machineID := range machineIDs {
		if machineID == 0 {
			continue
		}
		if _, ok := seen[machineID]; ok {
			continue
		}
		seen[machineID] = struct{}{}
		uniqueIDs = append(uniqueIDs, machineID)
	}
	if len(uniqueIDs) == 0 {
		return []Machine{}, nil
	}

	machines := []Machine{}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Where("id in ?", uniqueIDs).Find(&machines).Error; err != nil {
		return nil, err
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].ID < machines[j].ID })

	return machines, nil
}

func (h *Mirage) listMachinesByOrgIDs(orgIDs []int64) ([]Machine, error) {
	if len(orgIDs) == 0 {
		return []Machine{}, nil
	}

	seen := make(map[int64]struct{}, len(orgIDs))
	uniqueOrgIDs := make([]int64, 0, len(orgIDs))
	for _, orgID := range orgIDs {
		if orgID == 0 {
			continue
		}
		if _, ok := seen[orgID]; ok {
			continue
		}
		seen[orgID] = struct{}{}
		uniqueOrgIDs = append(uniqueOrgIDs, orgID)
	}
	if len(uniqueOrgIDs) == 0 {
		return []Machine{}, nil
	}

	users, err := h.ListUsersInOrgs(uniqueOrgIDs)
	if err != nil {
		return nil, err
	}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	if len(userIDs) == 0 {
		return []Machine{}, nil
	}

	machines := []Machine{}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Where("user_id in ?", userIDs).Find(&machines).Error; err != nil {
		return nil, err
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].ID < machines[j].ID })

	return machines, nil
}

func (h *Mirage) listMachinesByUserIDs(userIDs []int64) ([]Machine, error) {
	if len(userIDs) == 0 {
		return []Machine{}, nil
	}

	seen := make(map[int64]struct{}, len(userIDs))
	uniqueUserIDs := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		uniqueUserIDs = append(uniqueUserIDs, userID)
	}
	if len(uniqueUserIDs) == 0 {
		return []Machine{}, nil
	}

	machines := []Machine{}
	if err := h.db.Preload("AuthKey").Preload("AuthKey.User").Preload("User").Preload("User.Organization").Where("user_id in ?", uniqueUserIDs).Find(&machines).Error; err != nil {
		return nil, err
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].ID < machines[j].ID })

	return machines, nil
}

func (h *Mirage) ListMachineSharesBySourceMachine(machineID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("source_machine_id = ?", machineID).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) GetMachineShareByStableID(stableID string) (*MachineShare, error) {
	share := MachineShare{}
	if err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").First(&share, "stable_id = ?", strings.TrimSpace(stableID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMachineShareNotFound
		}
		return nil, err
	}

	return &share, nil
}

func (h *Mirage) ListAcceptedMachineSharesBySourceMachine(machineID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("source_machine_id = ? AND status = ? AND target_org_id <> 0", machineID, MachineShareStatusAccepted).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) ListAcceptedMachineSharesBySourceOrg(orgID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("source_org_id = ? AND status = ? AND target_org_id <> 0", orgID, MachineShareStatusAccepted).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) ListAcceptedMachineSharesByTargetOrg(orgID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("target_org_id = ? AND status = ?", orgID, MachineShareStatusAccepted).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) ListAcceptedMachineSharesByTargetUser(userID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("target_user_id = ? AND status = ?", userID, MachineShareStatusAccepted).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) listPendingMachineSharesByIdentity(identity string) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("target_identity = ? AND status = ?", normalizeExternalIdentity(identity), MachineShareStatusPending).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) ListPendingOrgInvitesByOrgID(orgID int64) ([]OrgInvite, error) {
	invites := []OrgInvite{}
	err := h.db.Preload("Org").Where("org_id = ? AND status = ?", orgID, OrgInviteStatusPending).Order("created_at asc").Find(&invites).Error
	if err != nil {
		return nil, err
	}

	return invites, nil
}

func (h *Mirage) ListOrgInvitesByOrgID(orgID int64) ([]OrgInvite, error) {
	invites := []OrgInvite{}
	err := h.db.Preload("Org").Where("org_id = ?", orgID).Order("created_at desc").Find(&invites).Error
	if err != nil {
		return nil, err
	}

	return invites, nil
}

func (h *Mirage) listPendingOrgInvitesByIdentity(identity string) ([]OrgInvite, error) {
	invites := []OrgInvite{}
	err := h.db.Preload("Org").Where("target_identity = ? AND status = ?", normalizeExternalIdentity(identity), OrgInviteStatusPending).Order("created_at asc").Find(&invites).Error
	if err != nil {
		return nil, err
	}

	return invites, nil
}

func (h *Mirage) ListUsersByNormalizedName(name string) ([]User, error) {
	users, err := h.ListUsers()
	if err != nil {
		return nil, err
	}

	needle := normalizeExternalIdentity(name)
	matched := make([]User, 0)
	for _, user := range users {
		if normalizeExternalIdentity(user.Name) == needle {
			matched = append(matched, user)
		}
	}

	return matched, nil
}

func (h *Mirage) GetOrgInviteByToken(inviteToken string) (*OrgInvite, error) {
	invite := OrgInvite{}
	if err := h.db.Preload("Org").First(&invite, "invite_token = ?", strings.TrimSpace(inviteToken)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrgInviteNotFound
		}
		return nil, err
	}

	return &invite, nil
}

func createUserInOrganizationInTx(tx *gorm.DB, name string, disName string, org *Organization) (*User, error) {
	if org == nil || org.ID == 0 {
		return nil, ErrOrgNotFound
	}

	user := User{
		Name:           name,
		Display_Name:   disName,
		OrganizationID: org.ID,
		Organization:   *org,
		Role:           RoleMember,
	}
	if err := tx.Create(&user).Error; err != nil {
		return nil, err
	}

	return &user, nil
}

func (h *Mirage) CreateOrgInvite(orgID int64, inviterUserID int64, targetIdentity string) (*OrgInvite, error) {
	normalizedIdentity := normalizeExternalIdentity(targetIdentity)
	if normalizedIdentity == "" {
		return nil, ErrOrgInviteTargetInvalid
	}

	org, err := h.GetOrgnaizationByID(orgID)
	if err != nil {
		return nil, err
	}

	users, err := h.ListUsersByNormalizedName(normalizedIdentity)
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		if user.OrganizationID == orgID {
			return nil, ErrOrgInviteTargetAlreadyInOrg
		}
		return nil, ErrOrgInviteTargetBelongsToOtherOrg
	}

	pendingInvites, err := h.listPendingOrgInvitesByIdentity(normalizedIdentity)
	if err != nil {
		return nil, err
	}
	if len(pendingInvites) > 0 {
		return nil, ErrOrgInviteTargetHasPendingInvite
	}

	invite := &OrgInvite{
		OrgID:          org.ID,
		Org:            *org,
		InviterUserID:  inviterUserID,
		TargetIdentity: normalizedIdentity,
		Status:         OrgInviteStatusPending,
	}
	if err := h.db.Create(invite).Error; err != nil {
		return nil, err
	}

	return invite, nil
}

func (h *Mirage) RevokeOrgInvite(inviteID int64, orgID int64) error {
	invite := OrgInvite{}
	if err := h.db.Preload("Org").First(&invite, "id = ?", inviteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrgInviteNotFound
		}
		return err
	}
	if invite.OrgID != orgID {
		return ErrOrgInviteNotFound
	}
	if invite.Status != OrgInviteStatusPending {
		return orgInviteStatusError(invite.Status)
	}

	now := time.Now().UTC()
	invite.Status = OrgInviteStatusRevoked
	invite.RevokedAt = &now

	return h.db.Save(&invite).Error
}

func (h *Mirage) resolvePendingInviteForIdentity(identity string) (*OrgInvite, *Organization, error) {
	invites, err := h.listPendingOrgInvitesByIdentity(identity)
	if err != nil {
		return nil, nil, err
	}
	if len(invites) == 0 {
		return nil, nil, nil
	}

	invite := invites[0]
	org, err := h.GetOrgnaizationByID(invite.OrgID)
	if err != nil {
		return nil, nil, err
	}

	return &invite, org, nil
}

func (h *Mirage) AcceptOrgInviteByToken(inviteToken string, userName string, userDisName string) (*OrgInvite, *User, error) {
	normalizedIdentity := normalizeExternalIdentity(userName)
	if normalizedIdentity == "" {
		return nil, nil, ErrOrgInviteTargetInvalid
	}
	if strings.TrimSpace(userDisName) == "" {
		userDisName = userName
	}

	invite, err := h.GetOrgInviteByToken(inviteToken)
	if err != nil {
		return nil, nil, err
	}
	if invite.TargetIdentity != normalizedIdentity {
		return nil, nil, ErrOrgInviteTargetMismatch
	}

	var invitedUser *User
	err = h.db.Transaction(func(tx *gorm.DB) error {
		lockedInvite := OrgInvite{}
		if err := tx.Preload("Org").First(&lockedInvite, "id = ?", invite.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrgInviteNotFound
			}
			return err
		}
		if lockedInvite.TargetIdentity != normalizedIdentity {
			return ErrOrgInviteTargetMismatch
		}
		if lockedInvite.Status != OrgInviteStatusPending {
			return orgInviteStatusError(lockedInvite.Status)
		}

		existingUsers, err := h.ListUsersByNormalizedName(normalizedIdentity)
		if err != nil {
			return err
		}
		for _, existingUser := range existingUsers {
			if existingUser.OrganizationID == lockedInvite.OrgID {
				return ErrOrgInviteTargetAlreadyInOrg
			}
			return ErrOrgInviteTargetBelongsToOtherOrg
		}

		org := Organization{}
		if err := tx.First(&org, "id = ?", lockedInvite.OrgID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrgNotFound
			}
			return err
		}

		invitedUser, err = createUserInOrganizationInTx(tx, normalizedIdentity, userDisName, &org)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		lockedInvite.Status = OrgInviteStatusAccepted
		lockedInvite.AcceptedUserID = invitedUser.ID
		lockedInvite.AcceptedAt = &now
		lockedInvite.RejectedAt = nil
		if err := tx.Save(&lockedInvite).Error; err != nil {
			return err
		}

		*invite = lockedInvite
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	h.setOrgLastStateChangeToNow(invite.OrgID)
	return invite, invitedUser, nil
}

func (h *Mirage) RejectOrgInviteByToken(inviteToken string, identity string) (*OrgInvite, error) {
	normalizedIdentity := normalizeExternalIdentity(identity)
	if normalizedIdentity == "" {
		return nil, ErrOrgInviteTargetInvalid
	}

	invite, err := h.GetOrgInviteByToken(inviteToken)
	if err != nil {
		return nil, err
	}
	if invite.TargetIdentity != normalizedIdentity {
		return nil, ErrOrgInviteTargetMismatch
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		lockedInvite := OrgInvite{}
		if err := tx.Preload("Org").First(&lockedInvite, "id = ?", invite.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrgInviteNotFound
			}
			return err
		}
		if lockedInvite.TargetIdentity != normalizedIdentity {
			return ErrOrgInviteTargetMismatch
		}
		if lockedInvite.Status != OrgInviteStatusPending {
			return orgInviteStatusError(lockedInvite.Status)
		}

		now := time.Now().UTC()
		lockedInvite.Status = OrgInviteStatusRejected
		lockedInvite.RejectedAt = &now
		lockedInvite.AcceptedAt = nil
		lockedInvite.AcceptedUserID = 0
		if err := tx.Save(&lockedInvite).Error; err != nil {
			return err
		}

		*invite = lockedInvite
		return nil
	})
	if err != nil {
		return nil, err
	}

	h.setOrgLastStateChangeToNow(invite.OrgID)
	return invite, nil
}

func (h *Mirage) CreateUserFromInvite(invite *OrgInvite, org *Organization, userName string, userDisName string) (*User, error) {
	if invite == nil || org == nil {
		return nil, ErrOrgInviteNotFound
	}

	users, err := h.ListUsersByNormalizedName(userName)
	if err != nil {
		return nil, err
	}
	for _, existingUser := range users {
		if existingUser.OrganizationID != org.ID {
			return nil, ErrOrgInviteTargetBelongsToOtherOrg
		}
	}

	var user *User
	err = h.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		lockedInvite := OrgInvite{}
		if err := tx.Preload("Org").First(&lockedInvite, "id = ?", invite.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrgInviteNotFound
			}
			return err
		}
		if lockedInvite.Status != OrgInviteStatusPending {
			return nil
		}

		user, err = createUserInOrganizationInTx(tx, userName, userDisName, org)
		if err != nil {
			return err
		}

		lockedInvite.Status = OrgInviteStatusAccepted
		lockedInvite.AcceptedUserID = user.ID
		lockedInvite.AcceptedAt = &now
		return tx.Save(&lockedInvite).Error
	})
	if err != nil {
		return nil, err
	}
	if user != nil {
		h.setOrgLastStateChangeToNow(org.ID)
	}

	return user, nil
}

func (h *Mirage) CreateMachineShare(sourceMachine *Machine, sourceUser *User, targetIdentity string) (*MachineShare, error) {
	if sourceMachine == nil || sourceUser == nil || sourceUser.CheckEmpty() {
		return nil, ErrMachineNotFound
	}

	normalizedIdentity := normalizeExternalIdentity(targetIdentity)
	if normalizedIdentity == "" {
		return nil, ErrMachineShareTargetInvalid
	}

	users, err := h.ListUsersByNormalizedName(normalizedIdentity)
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		if user.OrganizationID == sourceUser.OrganizationID {
			return nil, ErrMachineShareTargetAlreadyInOrg
		}
	}

	shares, err := h.ListMachineSharesBySourceMachine(sourceMachine.ID)
	if err != nil {
		return nil, err
	}
	for _, share := range shares {
		if share.Status != MachineShareStatusRevoked && share.TargetIdentity == normalizedIdentity {
			return &share, nil
		}
	}

	share := &MachineShare{
		SourceMachineID: sourceMachine.ID,
		SourceMachine:   *sourceMachine,
		SourceOrgID:     sourceMachine.User.OrganizationID,
		SourceUserID:    sourceUser.ID,
		TargetIdentity:  normalizedIdentity,
		Status:          MachineShareStatusPending,
	}
	if err := h.db.Create(share).Error; err != nil {
		return nil, err
	}

	h.setOrgLastStateChangeToNow(sourceMachine.User.OrganizationID)
	return share, nil
}

func (h *Mirage) hasAcceptedShareFromMachineToOrg(sourceMachineID int64, targetOrgID int64, excludeShareID int64) (bool, error) {
	var count int64
	query := h.db.Model(&MachineShare{}).Where("source_machine_id = ? AND target_org_id = ? AND status = ?", sourceMachineID, targetOrgID, MachineShareStatusAccepted)
	if excludeShareID != 0 {
		query = query.Where("id <> ?", excludeShareID)
	}
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (h *Mirage) hasAcceptedShareBetweenOrgs(sourceOrgID int64, targetOrgID int64, excludeShareID int64) (bool, error) {
	var count int64
	query := h.db.Model(&MachineShare{}).Where("source_org_id = ? AND target_org_id = ? AND status = ?", sourceOrgID, targetOrgID, MachineShareStatusAccepted)
	if excludeShareID != 0 {
		query = query.Where("id <> ?", excludeShareID)
	}
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (h *Mirage) hasAcceptedShareBetweenSourceOrgAndTargetUser(sourceOrgID int64, targetUserID int64, excludeShareID int64) (bool, error) {
	var count int64
	query := h.db.Model(&MachineShare{}).Where("source_org_id = ? AND target_user_id = ? AND status = ?", sourceOrgID, targetUserID, MachineShareStatusAccepted)
	if excludeShareID != 0 {
		query = query.Where("id <> ?", excludeShareID)
	}
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (h *Mirage) notifyMachineShareTopology(share *MachineShare, add bool) {
	if share == nil || share.TargetOrgID == 0 || share.SourceOrgID == 0 {
		return
	}

	sourceMachine, err := h.GetMachineByID(share.SourceMachineID)
	if err == nil {
		if add {
			h.NotifyNaviOrgNodesChange(share.TargetOrgID, sourceMachine.NodeKey, "")
		} else {
			remaining, remainingErr := h.hasAcceptedShareFromMachineToOrg(share.SourceMachineID, share.TargetOrgID, share.ID)
			if remainingErr != nil {
				log.Error().Err(remainingErr).Msg("failed to inspect remaining accepted machine shares")
			} else if !remaining {
				h.NotifyNaviOrgNodesChange(share.TargetOrgID, "", sourceMachine.NodeKey)
			}
		}
	}

	targetMachines, err := h.ListMachinesByUser(share.TargetUserID)
	if err != nil {
		log.Error().Err(err).Msg("failed to list target user machines for machine share topology update")
	} else if add {
		for _, machine := range targetMachines {
			h.NotifyNaviOrgNodesChange(share.SourceOrgID, machine.NodeKey, "")
		}
	} else {
		remaining, remainingErr := h.hasAcceptedShareBetweenSourceOrgAndTargetUser(share.SourceOrgID, share.TargetUserID, share.ID)
		if remainingErr != nil {
			log.Error().Err(remainingErr).Msg("failed to inspect remaining accepted user shares")
		} else if !remaining {
			for _, machine := range targetMachines {
				h.NotifyNaviOrgNodesChange(share.SourceOrgID, "", machine.NodeKey)
			}
		}
	}

	h.setOrgLastStateChangeToNow(share.SourceOrgID, share.TargetOrgID)
}

func (h *Mirage) acceptMachineShare(share *MachineShare, user *User) error {
	if share == nil || user == nil || user.CheckEmpty() {
		return ErrMachineShareNotFound
	}
	if normalizeExternalIdentity(user.Name) != share.TargetIdentity {
		return ErrMachineShareTargetMismatch
	}

	now := time.Now().UTC()
	wasAccepted := false
	err := h.db.Transaction(func(tx *gorm.DB) error {
		lockedShare := MachineShare{}
		if err := tx.First(&lockedShare, "id = ?", share.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMachineShareNotFound
			}
			return err
		}
		if lockedShare.TargetIdentity != normalizeExternalIdentity(user.Name) {
			return ErrMachineShareTargetMismatch
		}
		if lockedShare.Status == MachineShareStatusRevoked {
			return ErrMachineShareAlreadyRevoked
		}
		if lockedShare.Status == MachineShareStatusAccepted {
			return ErrMachineShareAlreadyAccepted
		}
		if lockedShare.Status == MachineShareStatusRejected {
			return ErrMachineShareAlreadyRejected
		}

		lockedShare.Status = MachineShareStatusAccepted
		lockedShare.TargetUserID = user.ID
		lockedShare.TargetOrgID = user.OrganizationID
		lockedShare.AcceptedAt = &now
		lockedShare.RejectedAt = nil
		if err := tx.Save(&lockedShare).Error; err != nil {
			return err
		}

		share.Status = lockedShare.Status
		share.TargetUserID = lockedShare.TargetUserID
		share.TargetOrgID = lockedShare.TargetOrgID
		share.AcceptedAt = lockedShare.AcceptedAt
		wasAccepted = true
		return nil
	})
	if err != nil {
		return err
	}
	if wasAccepted {
		h.notifyMachineShareTopology(share, true)
	}

	return nil
}

func (h *Mirage) rejectMachineShare(share *MachineShare, user *User) error {
	if share == nil || user == nil || user.CheckEmpty() {
		return ErrMachineShareNotFound
	}
	if normalizeExternalIdentity(user.Name) != share.TargetIdentity {
		return ErrMachineShareTargetMismatch
	}

	now := time.Now().UTC()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		lockedShare := MachineShare{}
		if err := tx.First(&lockedShare, "id = ?", share.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMachineShareNotFound
			}
			return err
		}
		if lockedShare.TargetIdentity != normalizeExternalIdentity(user.Name) {
			return ErrMachineShareTargetMismatch
		}
		if lockedShare.Status != MachineShareStatusPending {
			return machineShareStatusError(lockedShare.Status)
		}

		lockedShare.Status = MachineShareStatusRejected
		lockedShare.RejectedAt = &now
		lockedShare.AcceptedAt = nil
		lockedShare.TargetUserID = 0
		lockedShare.TargetOrgID = 0
		if err := tx.Save(&lockedShare).Error; err != nil {
			return err
		}

		share.Status = lockedShare.Status
		share.AcceptedAt = nil
		share.RejectedAt = lockedShare.RejectedAt
		share.TargetUserID = 0
		share.TargetOrgID = 0
		return nil
	})
	if err != nil {
		return err
	}

	h.setOrgLastStateChangeToNow(share.SourceOrgID)
	return nil
}

func (h *Mirage) AcceptPendingMachineSharesForUser(user *User) ([]MachineShare, error) {
	if user == nil || user.CheckEmpty() {
		return nil, ErrUserNotFound
	}

	shares, err := h.listPendingMachineSharesByIdentity(user.Name)
	if err != nil {
		return nil, err
	}
	accepted := make([]MachineShare, 0, len(shares))
	for i := range shares {
		share := shares[i]
		if err := h.acceptMachineShare(&share, user); err != nil {
			return accepted, err
		}
		accepted = append(accepted, share)
	}

	return accepted, nil
}

func (h *Mirage) AcceptMachineShareByToken(shareToken string, user *User) (*MachineShare, error) {
	share := MachineShare{}
	if err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").First(&share, "share_token = ?", strings.TrimSpace(shareToken)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMachineShareNotFound
		}
		return nil, err
	}
	if err := h.acceptMachineShare(&share, user); err != nil {
		return nil, err
	}

	return &share, nil
}

func (h *Mirage) RejectMachineShareByToken(shareToken string, user *User) (*MachineShare, error) {
	share := MachineShare{}
	if err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").First(&share, "share_token = ?", strings.TrimSpace(shareToken)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMachineShareNotFound
		}
		return nil, err
	}
	if err := h.rejectMachineShare(&share, user); err != nil {
		return nil, err
	}

	return &share, nil
}

func (h *Mirage) RevokeMachineShare(shareID int64, sourceOrgID int64) error {
	share := MachineShare{}
	if err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").First(&share, "id = ?", shareID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMachineShareNotFound
		}
		return err
	}
	if share.SourceOrgID != sourceOrgID {
		return ErrMachineShareNotFound
	}
	if share.Status == MachineShareStatusRevoked {
		return ErrMachineShareAlreadyRevoked
	}
	if share.Status == MachineShareStatusRejected {
		return ErrMachineShareAlreadyRejected
	}

	wasAccepted := share.Status == MachineShareStatusAccepted && share.TargetOrgID != 0
	now := time.Now().UTC()
	share.Status = MachineShareStatusRevoked
	share.RevokedAt = &now
	if err := h.db.Save(&share).Error; err != nil {
		return err
	}
	if wasAccepted {
		h.notifyMachineShareTopology(&share, false)
	} else {
		h.setOrgLastStateChangeToNow(share.SourceOrgID)
	}

	return nil
}

func (h *Mirage) ListSharedMachinesByTargetOrgID(orgID int64) ([]Machine, error) {
	shares, err := h.ListAcceptedMachineSharesByTargetOrg(orgID)
	if err != nil {
		return nil, err
	}
	machineIDs := make([]int64, 0, len(shares))
	for _, share := range shares {
		machineIDs = append(machineIDs, share.SourceMachineID)
	}
	machines, err := h.listMachinesByIDs(machineIDs)
	if err != nil {
		return nil, err
	}
	for i := range machines {
		machines[i].Shared = true
	}

	return machines, nil
}

func (h *Mirage) ListSharedMachinesByTargetUserID(userID int64) ([]Machine, error) {
	shares, err := h.ListAcceptedMachineSharesByTargetUser(userID)
	if err != nil {
		return nil, err
	}
	machineIDs := make([]int64, 0, len(shares))
	for _, share := range shares {
		machineIDs = append(machineIDs, share.SourceMachineID)
	}
	machines, err := h.listMachinesByIDs(machineIDs)
	if err != nil {
		return nil, err
	}
	for i := range machines {
		machines[i].Shared = true
	}

	return machines, nil
}

func (h *Mirage) ListShareeMachinesBySourceMachineID(machineID int64) ([]Machine, error) {
	shares, err := h.ListAcceptedMachineSharesBySourceMachine(machineID)
	if err != nil {
		return nil, err
	}
	targetUserIDs := make([]int64, 0, len(shares))
	for _, share := range shares {
		if share.TargetUserID != 0 {
			targetUserIDs = append(targetUserIDs, share.TargetUserID)
		}
	}
	machines, err := h.listMachinesByUserIDs(targetUserIDs)
	if err != nil {
		return nil, err
	}
	for i := range machines {
		machines[i].ShareeNode = true
	}

	return machines, nil
}

func (h *Mirage) ListVisibleMachinesByUserID(userID int64) ([]Machine, error) {
	user, err := h.GetUserByID(tailcfg.UserID(userID))
	if err != nil {
		return nil, err
	}
	ownedMachines, err := h.ListMachinesByOrgID(user.OrganizationID)
	if err != nil {
		return nil, err
	}
	sharedMachines, err := h.ListSharedMachinesByTargetUserID(userID)
	if err != nil {
		return nil, err
	}

	return mergeMachines(ownedMachines, sharedMachines), nil
}

func (h *Mirage) IsMachineVisibleToUser(machine *Machine, userID int64) (bool, error) {
	if machine == nil {
		return false, ErrMachineNotFound
	}
	user, err := h.GetUserByID(tailcfg.UserID(userID))
	if err != nil {
		return false, err
	}
	if machine.User.OrganizationID == user.OrganizationID {
		return true, nil
	}

	var count int64
	if err := h.db.Model(&MachineShare{}).Where("source_machine_id = ? AND target_user_id = ? AND status = ?", machine.ID, userID, MachineShareStatusAccepted).Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (h *Mirage) ListVisibleMachinesByOrgID(orgID int64) ([]Machine, error) {
	orgMachines, err := h.ListMachinesByOrgID(orgID)
	if err != nil {
		return nil, err
	}
	sharedMachines, err := h.ListSharedMachinesByTargetOrgID(orgID)
	if err != nil {
		return nil, err
	}

	return mergeMachines(orgMachines, sharedMachines), nil
}

func (h *Mirage) IsMachineVisibleToOrg(machine *Machine, orgID int64) (bool, error) {
	if machine == nil {
		return false, ErrMachineNotFound
	}
	if machine.User.OrganizationID == orgID {
		return true, nil
	}

	var count int64
	if err := h.db.Model(&MachineShare{}).Where("source_machine_id = ? AND target_org_id = ? AND status = ?", machine.ID, orgID, MachineShareStatusAccepted).Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

func (h *Mirage) ListPendingMachineSharesBySourceOrg(orgID int64) ([]MachineShare, error) {
	shares := []MachineShare{}
	err := h.db.Preload("SourceMachine").Preload("SourceMachine.User").Preload("SourceMachine.User.Organization").Where("source_org_id = ? AND status = ?", orgID, MachineShareStatusPending).Order("created_at asc").Find(&shares).Error
	if err != nil {
		return nil, err
	}

	return shares, nil
}

func (h *Mirage) ListShareConnectedOrgIDs(orgIDs ...int64) ([]int64, error) {
	if len(orgIDs) == 0 {
		return []int64{}, nil
	}

	unique := make(map[int64]struct{}, len(orgIDs))
	seed := make([]int64, 0, len(orgIDs))
	for _, orgID := range orgIDs {
		if orgID == 0 {
			continue
		}
		if _, ok := unique[orgID]; ok {
			continue
		}
		unique[orgID] = struct{}{}
		seed = append(seed, orgID)
	}
	if len(seed) == 0 {
		return []int64{}, nil
	}

	shares := []MachineShare{}
	if err := h.db.Where("status = ? AND (source_org_id in ? OR target_org_id in ?)", MachineShareStatusAccepted, seed, seed).Find(&shares).Error; err != nil {
		return nil, err
	}
	for _, share := range shares {
		if share.SourceOrgID != 0 {
			unique[share.SourceOrgID] = struct{}{}
		}
		if share.TargetOrgID != 0 {
			unique[share.TargetOrgID] = struct{}{}
		}
	}

	connected := make([]int64, 0, len(unique))
	for orgID := range unique {
		connected = append(connected, orgID)
	}
	sort.Slice(connected, func(i, j int) bool { return connected[i] < connected[j] })

	return connected, nil
}

func (h *Mirage) ListSharePeersForMachine(machine *Machine) ([]Machine, error) {
	if machine == nil {
		return nil, ErrMachineNotFound
	}

	sharedMachines, err := h.ListSharedMachinesByTargetUserID(machine.UserID)
	if err != nil {
		return nil, err
	}
	shareeMachines, err := h.ListShareeMachinesBySourceMachineID(machine.ID)
	if err != nil {
		return nil, err
	}

	filtered := make([]Machine, 0, len(sharedMachines)+len(shareeMachines))
	for _, peer := range mergeMachines(sharedMachines, shareeMachines) {
		if peer.ID == machine.ID {
			continue
		}
		filtered = append(filtered, peer)
	}

	return filtered, nil
}

func (h *Mirage) ListExternalSharedUsersByTargetUserID(userID int64) ([]User, error) {
	shares, err := h.ListAcceptedMachineSharesByTargetUser(userID)
	if err != nil {
		return nil, err
	}

	userIDs := make([]int64, 0, len(shares))
	for _, share := range shares {
		if share.SourceUserID != 0 {
			userIDs = append(userIDs, share.SourceUserID)
		}
	}
	if len(userIDs) == 0 {
		return []User{}, nil
	}

	users := []User{}
	if err := h.db.Preload("Organization").Where("id in ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}

	merged := make(map[int64]User)
	for _, user := range users {
		merged[user.ID] = user
	}
	result := make([]User, 0, len(merged))
	for _, user := range merged {
		result = append(result, user)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	return result, nil
}

func (h *Mirage) ListExternalSharedUsersByOrgID(orgID int64) ([]User, error) {
	shares, err := h.ListAcceptedMachineSharesByTargetOrg(orgID)
	if err != nil {
		return nil, err
	}

	userIDs := make([]int64, 0, len(shares))
	for _, share := range shares {
		if share.SourceUserID != 0 {
			userIDs = append(userIDs, share.SourceUserID)
		}
	}
	if len(userIDs) == 0 {
		return []User{}, nil
	}

	users := []User{}
	if err := h.db.Preload("Organization").Where("id in ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}

	merged := make(map[int64]User)
	for _, user := range users {
		merged[user.ID] = user
	}
	result := make([]User, 0, len(merged))
	for _, user := range merged {
		result = append(result, user)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	return result, nil
}

func (h *Mirage) CountAcceptedSharesBySourceMachine(machineID int64) (int, error) {
	var count int64
	if err := h.db.Model(&MachineShare{}).Where("source_machine_id = ? AND status = ?", machineID, MachineShareStatusAccepted).Count(&count).Error; err != nil {
		return 0, err
	}

	return int(count), nil
}

func (h *Mirage) CountActiveSharesBySourceMachine(machineID int64) (int, error) {
	var count int64
	if err := h.db.Model(&MachineShare{}).Where("source_machine_id = ? AND status in ?", machineID, []string{MachineShareStatusPending, MachineShareStatusAccepted}).Count(&count).Error; err != nil {
		return 0, err
	}

	return int(count), nil
}
