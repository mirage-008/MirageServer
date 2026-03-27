package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
	"tailscale.com/envknob"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
)

const (
	errEmptyPolicy                = Error("empty policy")
	errInvalidAction              = Error("invalid action")
	errInvalidGroup               = Error("invalid group")
	errInvalidTag                 = Error("invalid tag")
	errInvalidPortFormat          = Error("invalid port format")
	errWildcardIsNeeded           = Error("wildcard as port is required for the protocol")
	errInvalidAutoGroupSelfSource = Error("autogroup:self destination requires sources to be users, groups, wildcard, or autogroup:member only")
	errSSHAutogroupSelfSource     = Error("autogroup:self destination requires source to contain only users or groups, not tags or autogroup:tagged")
	errSSHTagSourceToUserDest     = Error("tags in SSH source cannot access user destinations")
	errSSHTagSourceToMemberDest   = Error("tags in SSH source cannot access autogroup:member")
)

const (
	Base8              = 8
	Base10             = 10
	BitSize16          = 16
	BitSize32          = 32
	BitSize64          = 64
	portRangeBegin     = 0
	portRangeEnd       = 65535
	expectedTokenItems = 2
)

const (
	AutoGroupPrefix   = "autogroup:"
	AutoGroupSelf     = "autogroup:self"
	AutoGroupOwner    = "autogroup:owner"
	AutoGroupMember   = "autogroup:member"
	AutoGroupTagged   = "autogroup:tagged"
	AutoGroupInternet = "autogroup:internet"
)

// For some reason golang.org/x/net/internal/iana is an internal package.
const (
	protocolICMP     = 1   // Internet Control Message
	protocolIGMP     = 2   // Internet Group Management
	protocolIPv4     = 4   // IPv4 encapsulation
	protocolTCP      = 6   // Transmission Control
	protocolEGP      = 8   // Exterior Gateway Protocol
	protocolIGP      = 9   // any private interior gateway (used by Cisco for their IGRP)
	protocolUDP      = 17  // User Datagram
	protocolGRE      = 47  // Generic Routing Encapsulation
	protocolESP      = 50  // Encap Security Payload
	protocolAH       = 51  // Authentication Header
	protocolIPv6ICMP = 58  // ICMP for IPv6
	protocolSCTP     = 132 // Stream Control Transmission Protocol
	ProtocolFC       = 133 // Fibre Channel
)

var featureEnableSSH = envknob.RegisterBool("MIRAGE_EXPERIMENTAL_FEATURE_SSH")

func (h *Mirage) SaveACLPolicy(path string) error {
	log.Debug().
		Str("func", "SaveACLPolicy").
		Str("path", path).
		Msg("Saving ACL policy back to path")

	aclData, err := json.Marshal(h.aclPolicy)
	if err != nil {
		return err
	}
	err = os.WriteFile(path, aclData, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (h *Mirage) SaveACLPolicyOfOrg(org *Organization) error {
	return h.db.Select("AclPolicy").Save(org).Error
}
func (h *Mirage) CreateDefaultACLPolicy() error {
	h.aclPolicy = &ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Protocol:     "",
			Sources:      []string{"*"},
			Destinations: []string{"*:*"},
		}},
	}
	return h.UpdateACLRules(0)
}

// LoadACLPolicy loads the ACL policy from the specify path, and generates the ACL rules.
func (h *Mirage) LoadACLPolicy(path string) error {
	log.Debug().
		Str("func", "LoadACLPolicy").
		Str("path", path).
		Msg("Loading ACL policy from path")

	policyFile, err := os.Open(path)
	if err != nil {
		return err
	}
	defer policyFile.Close()

	var policy ACLPolicy
	policyBytes, err := io.ReadAll(policyFile)
	if err != nil {
		return err
	}

	switch filepath.Ext(path) {
	case ".yml", ".yaml":
		log.Debug().
			Str("path", path).
			Bytes("file", policyBytes).
			Msg("Loading ACLs from YAML")

		err := yaml.Unmarshal(policyBytes, &policy)
		if err != nil {
			return err
		}

		log.Trace().
			Interface("policy", policy).
			Msg("Loaded policy from YAML")

	default:
		ast, err := hujson.Parse(policyBytes)
		if err != nil {
			return err
		}

		ast.Standardize()
		policyBytes = ast.Pack()
		err = json.Unmarshal(policyBytes, &policy)
		if err != nil {
			return err
		}
	}

	if policy.IsZero() {
		return errEmptyPolicy
	}

	h.aclPolicy = &policy

	return h.UpdateACLRules(0)
}

func (h *Mirage) UpdateACLRules(userId int64) error {
	machines, err := h.ListMachines()
	if err != nil {
		return err
	}

	if h.aclPolicy == nil {
		return errEmptyPolicy
	}

	rules, _, err := h.generateACLRules(machines, &User{}, *h.aclPolicy, h.cfg.OIDC.StripEmaildomain)

	if err != nil {
		return err
	}
	log.Trace().Interface("ACL", rules).Msg("ACL rules generated")
	h.aclRules = rules

	if featureEnableSSH() {
		sshRules, err := h.generateSSHRules(userId)
		if err != nil {
			return err
		}
		log.Trace().Interface("SSH", sshRules).Msg("SSH rules generated")
		if h.sshPolicy == nil {
			h.sshPolicy = &tailcfg.SSHPolicy{}
		}
		h.sshPolicy.Rules = sshRules
	} else if h.aclPolicy != nil && len(h.aclPolicy.SSHs) > 0 {
		log.Info().Msg("SSH ACLs has been defined, but MIRAGE_EXPERIMENTAL_FEATURE_SSH is not enabled, this is a unstable feature, check docs before activating")
	}

	return nil
}

func (h *Mirage) UpdateACLRulesOfOrg(org *Organization, user *User, targetMachine *Machine) (bool, error) {

	var enableSelf bool
	if org == nil || org.ID == 0 {
		return enableSelf, ErrOrgNotFound
	}
	machines, err := h.ListMachinesByOrgID(org.ID)
	if err != nil {
		return enableSelf, err
	}
	aclPolicy := org.AclPolicy
	rules, enableSelf, err := h.generateACLRules(machines, user, *aclPolicy, h.cfg.OIDC.StripEmaildomain)

	if err != nil {
		return enableSelf, err
	}
	log.Trace().Interface("ACL", rules).Msg("ACL rules generated")
	org.AclRules = rules

	if featureEnableSSH() {
		sshRules, err := h.generateSSHRulesOfOrg(machines, user.ID, org, targetMachine)

		if err != nil {
			return enableSelf, err
		}
		log.Trace().Interface("SSH", sshRules).Msg("SSH rules generated")
		if org.SshPolicy == nil {
			org.SshPolicy = &tailcfg.SSHPolicy{}
		}
		org.SshPolicy.Rules = sshRules
	} else if org.AclPolicy != nil && len(org.AclPolicy.SSHs) > 0 {
		log.Info().Msg("SSH ACLs has been defined, but MIRAGE_EXPERIMENTAL_FEATURE_SSH is not enabled, this is a unstable feature, check docs before activating")
	}

	return enableSelf, nil
}

func sshAcceptAction() tailcfg.SSHAction {
	return tailcfg.SSHAction{
		Message:                  "",
		Reject:                   false,
		Accept:                   true,
		SessionDuration:          0,
		AllowAgentForwarding:     false,
		HoldAndDelegate:          "",
		AllowLocalPortForwarding: true,
	}
}

func sshRejectAction() tailcfg.SSHAction {
	return tailcfg.SSHAction{
		Message:                  "",
		Reject:                   true,
		Accept:                   false,
		SessionDuration:          0,
		AllowAgentForwarding:     false,
		HoldAndDelegate:          "",
		AllowLocalPortForwarding: false,
	}
}

func cloneSSHRule(rule SSH) SSH {
	return SSH{
		Action:       rule.Action,
		Sources:      append([]string(nil), rule.Sources...),
		Destinations: append([]string(nil), rule.Destinations...),
		Users:        append([]string(nil), rule.Users...),
		CheckPeriod:  rule.CheckPeriod,
	}
}

func cloneSSHRules(rules []SSH) []SSH {
	cloned := make([]SSH, 0, len(rules))
	for _, rule := range rules {
		cloned = append(cloned, cloneSSHRule(rule))
	}

	return cloned
}

func normalizeSSHRuleItems(items []string) []string {
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

func normalizeSSHRule(rule SSH) SSH {
	return SSH{
		Action:       strings.ToLower(strings.TrimSpace(rule.Action)),
		Sources:      normalizeSSHRuleItems(rule.Sources),
		Destinations: normalizeSSHRuleItems(rule.Destinations),
		Users:        normalizeSSHRuleItems(rule.Users),
		CheckPeriod:  strings.TrimSpace(rule.CheckPeriod),
	}
}

func validateSSHRuleShape(rule SSH) error {
	if rule.Action == "" {
		return fmt.Errorf("SSH 规则动作不能为空")
	}
	if rule.Action != "accept" && rule.Action != "check" {
		return fmt.Errorf("SSH 规则动作无效")
	}
	if len(rule.Sources) == 0 {
		return fmt.Errorf("SSH 来源不能为空")
	}
	if len(rule.Destinations) == 0 {
		return fmt.Errorf("SSH 目标不能为空")
	}
	if len(rule.Users) == 0 {
		return fmt.Errorf("SSH 用户不能为空")
	}
	if rule.Action == "check" {
		if rule.CheckPeriod == "" {
			return fmt.Errorf("SSH 检查时长不能为空")
		}
		if _, err := sshCheckAction(rule.CheckPeriod); err != nil {
			return fmt.Errorf("SSH 检查时长无效")
		}
	}

	return nil
}

func validateSSHAliasReference(alias string, aclPolicy ACLPolicy, machines []Machine) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("SSH 别名不能为空")
	}
	if alias == "*" {
		return nil
	}
	if _, err := netip.ParseAddr(alias); err == nil {
		return nil
	}
	if _, err := netip.ParsePrefix(alias); err == nil {
		return nil
	}
	if _, ok := aclPolicy.Hosts[alias]; ok {
		return nil
	}
	if strings.HasPrefix(alias, AutoGroupPrefix) {
		switch alias {
		case AutoGroupSelf, AutoGroupMember, AutoGroupOwner, AutoGroupTagged, AutoGroupInternet:
			return nil
		default:
			return fmt.Errorf("SSH 别名无效")
		}
	}
	if strings.HasPrefix(alias, "group:") {
		if _, ok := aclPolicy.Groups[alias]; !ok {
			return errInvalidGroup
		}
		return nil
	}
	if strings.HasPrefix(alias, "tag:") {
		if _, ok := aclPolicy.TagOwners[alias]; ok {
			return nil
		}
		for _, machine := range machines {
			hi := machine.GetHostInfo()
			if contains(machine.ForcedTags, alias) || contains(hi.RequestTags, alias) {
				return nil
			}
		}
		return errInvalidTag
	}
	if strings.Contains(alias, ":") {
		return errInvalidPortFormat
	}

	return nil
}

func validateSSHRuleAliases(rule SSH, aclPolicy ACLPolicy, machines []Machine) error {
	for _, alias := range rule.Sources {
		if err := validateSSHAliasReference(alias, aclPolicy, machines); err != nil {
			return err
		}
	}
	for _, alias := range rule.Destinations {
		if err := validateSSHAliasReference(alias, aclPolicy, machines); err != nil {
			return err
		}
	}

	return validateSSHSourceDestinationCombination(rule, aclPolicy)
}

func validateSSHSourceDestinationCombination(rule SSH, aclPolicy ACLPolicy) error {
	srcHasTaggedEntities := false
	for _, src := range rule.Sources {
		switch {
		case strings.HasPrefix(src, "tag:"):
			srcHasTaggedEntities = true
		case src == AutoGroupTagged:
			srcHasTaggedEntities = true
		}
		if srcHasTaggedEntities {
			break
		}
	}
	if !srcHasTaggedEntities {
		return nil
	}

	for _, dst := range rule.Destinations {
		switch {
		case dst == AutoGroupSelf:
			return errSSHAutogroupSelfSource
		case dst == AutoGroupMember:
			return errSSHTagSourceToMemberDest
		case isSSHUsernameAlias(dst, aclPolicy):
			return errSSHTagSourceToUserDest
		}
	}

	return nil
}

func isSSHUsernameAlias(alias string, aclPolicy ACLPolicy) bool {
	alias = strings.TrimSpace(alias)
	if alias == "" || alias == "*" {
		return false
	}
	if _, err := netip.ParseAddr(alias); err == nil {
		return false
	}
	if _, err := netip.ParsePrefix(alias); err == nil {
		return false
	}
	if _, ok := aclPolicy.Hosts[alias]; ok {
		return false
	}
	if strings.HasPrefix(alias, AutoGroupPrefix) {
		return false
	}
	if strings.HasPrefix(alias, "group:") {
		return false
	}
	if strings.HasPrefix(alias, "tag:") {
		return false
	}

	return !strings.Contains(alias, ":")
}

func sshRuleValidationMessage(err error) string {
	switch {
	case errors.Is(err, errInvalidGroup):
		return "SSH 规则中引用了不存在或无效的用户组"
	case errors.Is(err, errInvalidTag):
		return "SSH 规则中引用了不存在或无效的标签"
	case errors.Is(err, errInvalidPortFormat), errors.Is(err, errWildcardIsNeeded):
		return "SSH 规则中的别名格式无效"
	default:
		return err.Error()
	}
}

func (h *Mirage) validateSSHRulesForPolicy(machines []Machine, userId int64, aclPolicy ACLPolicy, rules []SSH) error {
	for _, rule := range rules {
		rule = normalizeSSHRule(rule)
		if err := validateSSHRuleShape(rule); err != nil {
			return err
		}
		if err := validateSSHRuleAliases(rule, aclPolicy, machines); err != nil {
			return err
		}
	}

	_, err := h.generateSSHRulesWithPolicy(machines, userId, aclPolicy, rules, nil)
	return err
}

func (h *Mirage) validateSSHRulesForOrg(org *Organization, user *User, rules []SSH) error {
	if org == nil || org.ID == 0 {
		return ErrOrgNotFound
	}

	policy := ACLPolicy{}
	if org.AclPolicy != nil {
		policy = *org.AclPolicy
	}
	policy.SSHs = cloneSSHRules(rules)

	machines, err := h.ListMachinesByOrgID(org.ID)
	if err != nil {
		return err
	}

	return h.validateSSHRulesForPolicy(machines, user.ID, policy, policy.SSHs)
}

func parseSSHRuleID(raw string) (int, error) {
	return parseACLRuleID(raw)
}

func sshRuleDataWithID(id int, rule SSH) map[string]interface{} {
	cloned := cloneSSHRule(rule)
	return map[string]interface{}{
		"id":          id,
		"action":      cloned.Action,
		"src":         cloned.Sources,
		"dst":         cloned.Destinations,
		"users":       cloned.Users,
		"checkPeriod": cloned.CheckPeriod,
	}
}

func sshRuleResponseData(rules []SSH) []map[string]interface{} {
	res := make([]map[string]interface{}, 0, len(rules))
	for id, rule := range rules {
		res = append(res, sshRuleDataWithID(id, rule))
	}

	return res
}

func withUpdatedSSHRule(rules []SSH, id int, rule SSH) ([]SSH, bool) {
	if id < 0 || id >= len(rules) {
		return nil, false
	}
	updated := cloneSSHRules(rules)
	updated[id] = cloneSSHRule(rule)
	return updated, true
}

func withDeletedSSHRule(rules []SSH, id int) ([]SSH, bool) {
	if id < 0 || id >= len(rules) {
		return nil, false
	}
	updated := cloneSSHRules(rules)
	updated = append(updated[:id], updated[id+1:]...)
	return updated, true
}

func withCreatedSSHRule(rules []SSH, rule SSH) ([]SSH, int) {
	updated := cloneSSHRules(rules)
	updated = append(updated, cloneSSHRule(rule))
	return updated, len(updated) - 1
}

func sshRulesOrEmpty(policy *ACLPolicy) []SSH {
	if policy == nil || len(policy.SSHs) == 0 {
		return []SSH{}
	}

	return cloneSSHRules(policy.SSHs)
}

func ensureSSHPolicy(policy **ACLPolicy) {
	if *policy == nil {
		*policy = &ACLPolicy{}
	}
	if (*policy).SSHs == nil {
		(*policy).SSHs = make([]SSH, 0)
	}
}

func sshRuleByID(rules []SSH, id int) (SSH, bool) {
	if id < 0 || id >= len(rules) {
		return SSH{}, false
	}

	return cloneSSHRule(rules[id]), true
}

func sshRuleListResult(rules []SSH) map[string]interface{} {
	return map[string]interface{}{
		"rules": sshRuleResponseData(rules),
	}
}

func sshRuleStateValue(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func sshRuleStored(rule SSH) SSH {
	return normalizeSSHRule(rule)
}

func sshRulesLength(rules []*tailcfg.SSHRule) int {
	return len(rules)
}

func sshRulesGet(rules []*tailcfg.SSHRule, index int) *tailcfg.SSHRule {
	if index < 0 || index >= len(rules) {
		return nil
	}
	return rules[index]
}

func sshRulesAny(rules []*tailcfg.SSHRule) bool {
	return len(rules) > 0
}

func sshRulesNone(rules []*tailcfg.SSHRule) bool {
	return len(rules) == 0
}

func sshRuleCheckDuration(rule *tailcfg.SSHRule) time.Duration {
	if rule == nil || rule.Action == nil {
		return 0
	}
	return rule.Action.SessionDuration
}

func sshRulePrincipalList(rule *tailcfg.SSHRule) []string {
	if rule == nil {
		return nil
	}
	ips := make([]string, 0, len(rule.Principals))
	for _, principal := range rule.Principals {
		if principal == nil {
			continue
		}
		ips = append(ips, principal.NodeIP)
	}
	return ips
}

func sshRuleUserAllowed(rule *tailcfg.SSHRule, user string) bool {
	if rule == nil {
		return false
	}
	value, ok := rule.SSHUsers[user]
	return ok && value == "="
}

func sshRuleCanForward(rule *tailcfg.SSHRule) bool {
	return rule != nil && rule.Action != nil && rule.Action.AllowLocalPortForwarding
}

func sshRuleIsAccept(rule *tailcfg.SSHRule) bool {
	return rule != nil && rule.Action != nil && rule.Action.Accept
}

func sshRuleIsReject(rule *tailcfg.SSHRule) bool {
	return rule != nil && rule.Action != nil && rule.Action.Reject
}

func sshRuleHasPrincipals(rule *tailcfg.SSHRule, ips []string) bool {
	current := sshRulePrincipalList(rule)
	if len(current) != len(ips) {
		return false
	}
	for i := range current {
		if current[i] != ips[i] {
			return false
		}
	}
	return true
}

func sshRuleResponse(id int, rule SSH) map[string]interface{} {
	return sshRuleDataWithID(id, rule)
}

func sshRulesResponse(rules []SSH) map[string]interface{} {
	return sshRuleListResult(rules)
}

func sshRuleNormalize(rule SSH) SSH {
	return normalizeSSHRule(rule)
}

func sshRuleUpdate(rules []SSH, id int, rule SSH) ([]SSH, bool) {
	return withUpdatedSSHRule(rules, id, rule)
}

func sshRuleDelete(rules []SSH, id int) ([]SSH, bool) {
	return withDeletedSSHRule(rules, id)
}

func sshRuleCreate(rules []SSH, rule SSH) ([]SSH, int) {
	return withCreatedSSHRule(rules, rule)
}

func sshRuleLookup(rules []SSH, id int) (SSH, bool) {
	return sshRuleByID(rules, id)
}

func sshRuleEnsurePolicy(policy **ACLPolicy) {
	ensureSSHPolicy(policy)
}

func sshRuleID(raw string) (int, error) {
	return parseSSHRuleID(raw)
}

func sshRuleErr(err error) string {
	return sshRuleValidationMessage(err)
}

func sshRuleActionCheck(rule *tailcfg.SSHRule) time.Duration {
	return sshRuleCheckDuration(rule)
}

func sshRuleActionHasUser(rule *tailcfg.SSHRule, user string) bool {
	return sshRuleUserAllowed(rule, user)
}

func sshRuleActionForward(rule *tailcfg.SSHRule) bool {
	return sshRuleCanForward(rule)
}

func sshRuleActionAccept(rule *tailcfg.SSHRule) bool {
	return sshRuleIsAccept(rule)
}

func sshRuleActionReject(rule *tailcfg.SSHRule) bool {
	return sshRuleIsReject(rule)
}

func sshRuleActionLen(rules []*tailcfg.SSHRule) int {
	return sshRulesLength(rules)
}

func sshRuleActionAt(rules []*tailcfg.SSHRule, index int) *tailcfg.SSHRule {
	return sshRulesGet(rules, index)
}

func sshRuleActionAny(rules []*tailcfg.SSHRule) bool {
	return sshRulesAny(rules)
}

func sshRuleActionNone(rules []*tailcfg.SSHRule) bool {
	return sshRulesNone(rules)
}

func sshRuleActionIPMatch(rule *tailcfg.SSHRule, ips []string) bool {
	return sshRuleHasPrincipals(rule, ips)
}

func sshRuleActionUsers(rule *tailcfg.SSHRule) map[string]string {
	if rule == nil {
		return nil
	}
	return rule.SSHUsers
}

func sshRuleActionIPs(rule *tailcfg.SSHRule) []string {
	return sshRulePrincipalList(rule)
}

func sshRuleActionUser(rule *tailcfg.SSHRule, user string) bool {
	return sshRuleUserAllowed(rule, user)
}

func sshRuleActionDuration(rule *tailcfg.SSHRule) time.Duration {
	return sshRuleCheckDuration(rule)
}

func sshRuleTypeWrapper(h *Mirage, machines []Machine, userId int64, aclPolicy ACLPolicy, rule SSH, targetMachine *Machine) (bool, error) {
	return h.sshRuleMatchesTargetMachine(machines, userId, aclPolicy, rule, targetMachine)
}

func sshRuleTypeActionFor(index int, rule SSH) (*tailcfg.SSHAction, error) {
	return sshActionForRule(index, rule)
}

func sshRuleTypeAppendPrincipals(principals []*tailcfg.SSHPrincipal, expandedSrcs []string) []*tailcfg.SSHPrincipal {
	return appendSSHPrincipals(principals, expandedSrcs)
}

func sshRuleTypeUsersMap(rule SSH) map[string]string {
	return buildSSHUsersMap(rule.Users)
}

func sshActionForRule(index int, sshACL SSH) (*tailcfg.SSHAction, error) {
	action := sshRejectAction()
	switch sshACL.Action {
	case "accept":
		action = sshAcceptAction()
	case "check":
		checkAction, err := sshCheckAction(sshACL.CheckPeriod)
		if err != nil {
			log.Error().
				Msgf("Error parsing SSH %d, check action with unparsable duration '%s'", index, sshACL.CheckPeriod)
			return nil, err
		}
		action = *checkAction
	default:
		log.Error().
			Msgf("Error parsing SSH %d, unknown action '%s'", index, sshACL.Action)
		return nil, fmt.Errorf("Error parsing SSH %d, unknown action '%s'", index, sshACL.Action)
	}

	return &action, nil
}

func appendSSHPrincipals(principals []*tailcfg.SSHPrincipal, expandedSrcs []string) []*tailcfg.SSHPrincipal {
	seen := make(map[string]struct{}, len(principals)+len(expandedSrcs))
	for _, principal := range principals {
		if principal != nil && principal.NodeIP != "" {
			seen[principal.NodeIP] = struct{}{}
		}
	}
	for _, expandedSrc := range expandedSrcs {
		if _, ok := seen[expandedSrc]; ok {
			continue
		}
		seen[expandedSrc] = struct{}{}
		principals = append(principals, &tailcfg.SSHPrincipal{NodeIP: expandedSrc})
	}

	return principals
}

func buildSSHUsersMap(users []string) map[string]string {
	userMap := make(map[string]string, len(users))
	for _, user := range users {
		userMap[user] = "="
	}

	return userMap
}

func (h *Mirage) sshRuleMatchesTargetMachine(machines []Machine, userId int64, aclPolicy ACLPolicy, sshACL SSH, targetMachine *Machine) (bool, error) {
	matched := targetMachine == nil
	var targetIPs []string
	if targetMachine != nil {
		targetIPs = targetMachine.IPAddresses.ToStringSlice()
	}

	for _, rawDst := range sshACL.Destinations {
		expandedDsts, err := h.expandAlias(
			false,
			machines,
			userId,
			aclPolicy,
			rawDst,
			h.cfg.OIDC.StripEmaildomain,
		)
		if err != nil {
			return false, err
		}
		if targetMachine != nil && containsAddresses(expandedDsts, targetIPs) {
			matched = true
		}
	}

	return matched, nil
}

func (h *Mirage) generateSSHRulesWithPolicy(machines []Machine, userId int64, aclPolicy ACLPolicy, sshACLs []SSH, targetMachine *Machine) ([]*tailcfg.SSHRule, error) {
	rules := []*tailcfg.SSHRule{}
	for index, rawRule := range sshACLs {
		sshACL := normalizeSSHRule(rawRule)
		if err := validateSSHRuleShape(sshACL); err != nil {
			return nil, err
		}
		if err := validateSSHRuleAliases(sshACL, aclPolicy, machines); err != nil {
			return nil, err
		}

		matched, err := h.sshRuleMatchesTargetMachine(machines, userId, aclPolicy, sshACL, targetMachine)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}

		action, err := sshActionForRule(index, sshACL)
		if err != nil {
			return nil, err
		}

		principals := make([]*tailcfg.SSHPrincipal, 0, len(sshACL.Sources))
		for innerIndex, rawSrc := range sshACL.Sources {
			expandedSrcs, err := h.expandAlias(
				false,
				machines,
				userId,
				aclPolicy,
				rawSrc,
				h.cfg.OIDC.StripEmaildomain,
			)
			if err != nil {
				log.Error().Msgf("Error parsing SSH %d, Source %d", index, innerIndex)
				return nil, err
			}
			principals = appendSSHPrincipals(principals, expandedSrcs)
		}

		rules = append(rules, &tailcfg.SSHRule{
			RuleExpires: nil,
			Principals:  principals,
			SSHUsers:    buildSSHUsersMap(sshACL.Users),
			Action:      action,
		})
	}

	return rules, nil
}

func (h *Mirage) generateSSHRulesOfOrg(machines []Machine, userId int64, org *Organization, targetMachine *Machine) ([]*tailcfg.SSHRule, error) {
	if org == nil || org.ID == 0 {
		return nil, ErrOrgNotFound
	}
	if org.AclPolicy == nil {
		return []*tailcfg.SSHRule{}, nil
	}

	return h.generateSSHRulesWithPolicy(machines, userId, *org.AclPolicy, org.AclPolicy.SSHs, targetMachine)
}

func (h *Mirage) generateACLRules(
	machines []Machine,
	user *User,
	aclPolicy ACLPolicy,
	stripEmaildomain bool,
) ([]tailcfg.FilterRule, bool, error) {
	rules := []tailcfg.FilterRule{}
	enableSelf := false
Loop:
	for index, acl := range aclPolicy.ACLs {
		useSelf := false

		if acl.Action != "accept" {
			return nil, enableSelf, errInvalidAction
		}

		if err := validateACLSourceDestCombination(aclPolicy, acl); err != nil {
			return nil, enableSelf, err
		}

		protocols, needsWildcard, err := parseProtocol(acl.Protocol)
		if err != nil {
			log.Error().
				Msgf("Error parsing ACL %d. protocol unknown %s", index, acl.Protocol)

			return nil, enableSelf, err
		}

		if containsSubStr(acl.Destinations, AutoGroupSelf) {
			if containsStr(acl.Sources, "*") {
				useSelf = true
			} else {
			LoopForSelf:
				for _, alias := range acl.Sources {
					if strings.HasPrefix(alias, "group:") {
						users, err := expandGroup(aclPolicy, acl.Sources[0], stripEmaildomain)
						if err != nil {
							log.Error().
								Msgf("Error expand group %s ", acl.Sources[0])
							continue LoopForSelf
						}
						if containsStr(users, user.Name) {
							useSelf = true
							break LoopForSelf
						}
					} else if alias == user.Name || alias == AutoGroupMember {
						useSelf = true
						break LoopForSelf
					}
				}
			}
			if !useSelf {
				continue Loop
			}
			enableSelf = true
		}
		destPorts := []tailcfg.NetPortRange{}
		for innerIndex, dest := range acl.Destinations {
			if isAutoGroupInternetDestination(dest) {
				continue
			}

			dests, err := h.generateACLPolicyDest(
				machines,
				user.ID,
				aclPolicy,
				dest,
				needsWildcard,
				stripEmaildomain,
			)
			if err != nil {
				log.Error().
					Msgf("Error parsing ACL %d, Destination %d", index, innerIndex)

				return nil, enableSelf, err
			}
			destPorts = append(destPorts, dests...)
		}
		if len(destPorts) == 0 {
			continue
		}

		srcIPs := []string{}
		// 如果dest里面配置了autogroup:self,且src的作用域包含了user
		if useSelf {

			/*
				for _, dest := range destPorts {
					srcIPs = append(srcIPs, dest.IP)
				}
			*/
			// src 按照autogroup:self的规则来解析
			srcs, err := h.generateACLPolicySrc(machines, user.ID, aclPolicy, AutoGroupSelf, stripEmaildomain)

			if err != nil {
				log.Error().
					Msgf("Error parsing ACL %d, Source %d", index, 0)

				return nil, enableSelf, err
			}
			srcIPs = append(srcIPs, srcs...)
		} else {
			for innerIndex, src := range acl.Sources {
				srcs, err := h.generateACLPolicySrc(machines, user.ID, aclPolicy, src, stripEmaildomain)

				if err != nil {
					log.Error().
						Msgf("Error parsing ACL %d, Source %d", index, innerIndex)

					return nil, enableSelf, err
				}
				srcIPs = append(srcIPs, srcs...)
			}
		}
		rules = append(rules, tailcfg.FilterRule{
			SrcIPs:   srcIPs,
			DstPorts: destPorts,
			IPProto:  protocols,
		})
	}

	return rules, enableSelf, nil
}

func validateACLSourceDestCombination(aclPolicy ACLPolicy, acl ACL) error {
	hasAutoGroupSelf := false
	for _, dest := range acl.Destinations {
		if isAutoGroupSelfDestination(dest) {
			hasAutoGroupSelf = true
			break
		}
	}
	if !hasAutoGroupSelf {
		return nil
	}

	for _, src := range acl.Sources {
		if !isValidAutoGroupSelfSource(aclPolicy, src) {
			return errInvalidAutoGroupSelfSource
		}
	}

	return nil
}

func isValidAutoGroupSelfSource(aclPolicy ACLPolicy, src string) bool {
	switch {
	case src == "*":
		return true
	case strings.HasPrefix(src, "group:"):
		return true
	case src == AutoGroupMember:
		return true
	case strings.HasPrefix(src, AutoGroupPrefix):
		return false
	case strings.HasPrefix(src, "tag:"):
		return false
	}

	if _, ok := aclPolicy.Hosts[src]; ok {
		return false
	}
	if _, err := netip.ParseAddr(src); err == nil {
		return false
	}
	if _, err := netip.ParsePrefix(src); err == nil {
		return false
	}

	return true
}

func destinationAlias(dest string) (string, error) {
	tokens := strings.Split(dest, ":")
	if len(tokens) < expectedTokenItems || len(tokens) > 3 {
		return "", errInvalidPortFormat
	}
	if len(tokens) == expectedTokenItems {
		return tokens[0], nil
	}

	return fmt.Sprintf("%s:%s", tokens[0], tokens[1]), nil
}

func isAutoGroupSelfDestination(dest string) bool {
	alias, err := destinationAlias(dest)
	return err == nil && alias == AutoGroupSelf
}

func isAutoGroupInternetDestination(dest string) bool {
	alias, err := destinationAlias(dest)
	return err == nil && alias == AutoGroupInternet
}

func (h *Mirage) generateSSHRules(userId int64) ([]*tailcfg.SSHRule, error) {
	if h.aclPolicy == nil {
		return nil, errEmptyPolicy
	}

	machines, err := h.ListMachines()
	if err != nil {
		return nil, err
	}

	return h.generateSSHRulesWithPolicy(machines, userId, *h.aclPolicy, h.aclPolicy.SSHs, nil)
}

func sshCheckAction(duration string) (*tailcfg.SSHAction, error) {
	sessionLength, err := time.ParseDuration(duration)
	if err != nil {
		return nil, err
	}

	return &tailcfg.SSHAction{
		Message:                  "",
		Reject:                   false,
		Accept:                   true,
		SessionDuration:          sessionLength,
		AllowAgentForwarding:     false,
		HoldAndDelegate:          "",
		AllowLocalPortForwarding: true,
	}, nil
}

func (h *Mirage) generateACLPolicySrc(
	machines []Machine,
	userId int64,
	aclPolicy ACLPolicy,
	src string,
	stripEmaildomain bool,
) ([]string, error) {
	return h.expandAlias(false, machines, userId, aclPolicy, src, stripEmaildomain)
}

func (h *Mirage) generateACLPolicyDest(
	machines []Machine,
	userId int64,
	aclPolicy ACLPolicy,
	dest string,
	needsWildcard bool,
	stripEmaildomain bool,
) ([]tailcfg.NetPortRange, error) {
	tokens := strings.Split(dest, ":")
	if len(tokens) < expectedTokenItems || len(tokens) > 3 {
		return nil, errInvalidPortFormat
	}

	var alias string
	// We can have here stuff like:
	// git-server:*
	// 192.168.1.0/24:22
	// tag:montreal-webserver:80,443
	// tag:api-server:443
	// example-host-1:*
	if len(tokens) == expectedTokenItems {
		alias = tokens[0]
	} else {
		alias = fmt.Sprintf("%s:%s", tokens[0], tokens[1])
	}

	var (
		expanded []string
		err      error
	)
	if alias == "*" {
		expanded = []string{"*"}
	} else {
		expanded, err = h.expandAlias(
			h.cfg.AllowRouteDueToMachine,
			machines,
			userId,
			aclPolicy,
			alias,
			stripEmaildomain,
		)
		if err != nil {
			return nil, err
		}
	}
	ports, err := expandPorts(tokens[len(tokens)-1], needsWildcard)
	if err != nil {
		return nil, err
	}

	dests := []tailcfg.NetPortRange{}
	for _, d := range expanded {
		for _, p := range *ports {
			pr := tailcfg.NetPortRange{
				IP:    d,
				Ports: p,
			}
			dests = append(dests, pr)
		}
	}

	return dests, nil
}

// parseProtocol reads the proto field of the ACL and generates a list of
// protocols that will be allowed, following the IANA IP protocol number
// https://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml
//
// If the ACL proto field is empty, it allows ICMPv4, ICMPv6, TCP, and UDP,
// as per Tailscale behaviour (see tailcfg.FilterRule).
//
// Also returns a boolean indicating if the protocol
// requires all the destinations to use wildcard as port number (only TCP,
// UDP and SCTP support specifying ports).
func parseProtocol(protocol string) ([]int, bool, error) {
	switch protocol {
	case "":
		return nil, false, nil
	case "igmp":
		return []int{protocolIGMP}, true, nil
	case "ipv4", "ip-in-ip":
		return []int{protocolIPv4}, true, nil
	case "tcp":
		return []int{protocolTCP}, false, nil
	case "egp":
		return []int{protocolEGP}, true, nil
	case "igp":
		return []int{protocolIGP}, true, nil
	case "udp":
		return []int{protocolUDP}, false, nil
	case "gre":
		return []int{protocolGRE}, true, nil
	case "esp":
		return []int{protocolESP}, true, nil
	case "ah":
		return []int{protocolAH}, true, nil
	case "sctp":
		return []int{protocolSCTP}, false, nil
	case "icmp":
		return []int{protocolICMP, protocolIPv6ICMP}, true, nil

	default:
		protocolNumber, err := strconv.Atoi(protocol)
		if err != nil {
			return nil, false, err
		}
		needsWildcard := protocolNumber != protocolTCP &&
			protocolNumber != protocolUDP &&
			protocolNumber != protocolSCTP

		return []int{protocolNumber}, needsWildcard, nil
	}
}

func (h *Mirage) expandMachineRoutes(machine Machine) []string {
	routeIPs := []string{}
	routeList, err := h.GetMachineRoutes(&machine)
	if err != nil {
		return routeIPs
	}
	for _, route := range routeList {
		if !route.Enabled {
			continue
		}
		if !route.isExitRoute() && !route.IsPrimary {
			continue
		}
		routeIPs = append(routeIPs, netip.Prefix(route.Prefix).String())
	}

	return routeIPs
}

// expandalias has an input of either
// - a user
// - a group
// - a tag
// - a host
// and transform these in IPAddresses.
func (h *Mirage) expandAlias(
	autoAddRoute bool,
	machines []Machine,
	userId int64,
	aclPolicy ACLPolicy,
	alias string,
	stripEmailDomain bool,
) (ips []string, resErr error) {
	defer func() {
		ips = lo.Uniq(ips)
	}()
	if alias == "*" {
		ips = []string{
			tsaddr.CGNATRange().String(),
			tsaddr.TailscaleULARange().String(),
		}
		return
	}

	log.Debug().
		Str("alias", alias).
		Msg("Expanding")

		// autogroup
	if strings.HasPrefix(alias, AutoGroupPrefix) {
		// 处理 autogroup:self
		if alias == AutoGroupSelf {
			nodes, err := h.ListMachinesByUser(userId)
			if err != nil {
				resErr = err
				return
			}
			for _, node := range nodes {
				if machineHasTagIdentity(aclPolicy, node, stripEmailDomain) {
					continue
				}
				ips = append(ips, node.IPAddresses.ToStringSlice()...)
				if autoAddRoute {
					ips = append(ips, h.expandMachineRoutes(node)...)
				}
			}
		} else if alias == AutoGroupMember {
			for _, machine := range machines {
				if machineHasTagIdentity(aclPolicy, machine, stripEmailDomain) {
					continue
				}
				ips = append(ips, machine.IPAddresses.ToStringSlice()...)
				if autoAddRoute {
					ips = append(ips, h.expandMachineRoutes(machine)...)
				}
			}
		} else if alias == AutoGroupTagged {
			for _, machine := range machines {
				if !machineHasTagIdentity(aclPolicy, machine, stripEmailDomain) {
					continue
				}
				ips = append(ips, machine.IPAddresses.ToStringSlice()...)
				if autoAddRoute {
					ips = append(ips, h.expandMachineRoutes(machine)...)
				}
			}
			// 处理 autogroup:owner
		} else if alias == AutoGroupOwner {
			for _, machine := range machines {
				if machine.User.Role == RoleOwner {
					ips = append(ips, machine.IPAddresses.ToStringSlice()...)
					if autoAddRoute {
						ips = append(ips, h.expandMachineRoutes(machine)...)
					}
				}
			}

			// 处理 autogroup:internet
		} else if alias == AutoGroupInternet {
			ips = append(ips, InternetIpLists...)
		}
		return
	}

	if strings.HasPrefix(alias, "group:") {
		users, err := expandGroup(aclPolicy, alias, stripEmailDomain)
		if err != nil {
			resErr = err
			return
		}
		for _, n := range users {
			nodes := filterMachinesByUser(machines, n)
			for _, node := range nodes {
				ips = append(ips, node.IPAddresses.ToStringSlice()...)
				if autoAddRoute {
					ips = append(ips, h.expandMachineRoutes(node)...)
				}
			}
		}
		return
	}

	if strings.HasPrefix(alias, "tag:") {
		// check for forced tags
		for _, machine := range machines {
			if contains(machine.ForcedTags, alias) {
				ips = append(ips, machine.IPAddresses.ToStringSlice()...)
				if autoAddRoute {
					ips = append(ips, h.expandMachineRoutes(machine)...)
				}
			}
		}

		// find tag owners
		owners, err := expandTagOwners(aclPolicy, alias, stripEmailDomain)
		if err != nil {
			if errors.Is(err, errInvalidTag) {
				if len(ips) == 0 {
					resErr = fmt.Errorf(
						"%w. %v isn't owned by a TagOwner and no forced tags are defined",
						errInvalidTag,
						alias,
					)
					return
				}
				return
			} else {
				resErr = err
				return
			}
		}

		// filter out machines per tag owner
		for _, user := range owners {
			machines := filterMachinesByUser(machines, user)
			for _, machine := range machines {
				hi := machine.GetHostInfo()
				if contains(hi.RequestTags, alias) {
					ips = append(ips, machine.IPAddresses.ToStringSlice()...)
					if autoAddRoute {
						ips = append(ips, h.expandMachineRoutes(machine)...)
					}
				}
			}
		}

		return
	}

	// if alias is a user
	nodes := filterMachinesByUser(machines, alias)
	nodes = excludeCorrectlyTaggedNodes(aclPolicy, nodes, alias, stripEmailDomain)

	for _, n := range nodes {
		ips = append(ips, n.IPAddresses.ToStringSlice()...)
		if autoAddRoute {
			ips = append(ips, h.expandMachineRoutes(n)...)
		}
	}
	if len(ips) > 0 {
		return
	}

	// if alias is an host
	if host, ok := aclPolicy.Hosts[alias]; ok {
		ips = append(ips, host.String())
		if autoAddRoute {
			mlist := h.GetMachinesInPrefix(host)
			for _, m := range mlist {
				ips = append(ips, h.expandMachineRoutes(m)...)
			}
		}
		return ips, nil
	}

	// if alias is an IP
	ip, err := netip.ParseAddr(alias)
	if err == nil {
		ips = append(ips, ip.String())
		if autoAddRoute {
			m := h.GetMachineByIP(ip)
			if m != nil {
				ips = append(ips, h.expandMachineRoutes(*m)...)
			}
		}
		return ips, nil
	}

	// if alias is an CIDR
	cidr, err := netip.ParsePrefix(alias)
	if err == nil {
		ips = append(ips, cidr.String())
		if autoAddRoute {
			mlist := h.GetMachinesInPrefix(cidr)
			for _, m := range mlist {
				ips = append(ips, h.expandMachineRoutes(m)...)
			}
		}
		return ips, nil
	}

	log.Debug().Msgf("No IPs found with the alias %v", alias)

	return
}

func machineHasTagIdentity(aclPolicy ACLPolicy, machine Machine, stripEmailDomain bool) bool {
	if len(machine.ForcedTags) > 0 {
		return true
	}

	validTags, _ := getTags(&aclPolicy, machine, stripEmailDomain)
	return len(validTags) > 0
}

// excludeCorrectlyTaggedNodes will remove from the list of input nodes the ones
// that are correctly tagged since they should not be listed as being in the user
// we assume in this function that we only have nodes from 1 user.
func excludeCorrectlyTaggedNodes(
	aclPolicy ACLPolicy,
	nodes []Machine,
	user string,
	stripEmailDomain bool,
) []Machine {
	out := []Machine{}
	tags := []string{}
	for tag := range aclPolicy.TagOwners {
		owners, _ := expandTagOwners(aclPolicy, user, stripEmailDomain)
		ns := append(owners, user)
		if contains(ns, user) {
			tags = append(tags, tag)
		}
	}
	// for each machine if tag is in tags list, don't append it.
	for _, machine := range nodes {
		hi := machine.GetHostInfo()

		found := false
		for _, t := range hi.RequestTags {
			if contains(tags, t) {
				found = true

				break
			}
		}
		if len(machine.ForcedTags) > 0 {
			found = true
		}
		if !found {
			out = append(out, machine)
		}
	}

	return out
}

func expandPorts(portsStr string, needsWildcard bool) (*[]tailcfg.PortRange, error) {
	if portsStr == "*" {
		return &[]tailcfg.PortRange{
			{First: portRangeBegin, Last: portRangeEnd},
		}, nil
	}

	if needsWildcard {
		return nil, errWildcardIsNeeded
	}

	ports := []tailcfg.PortRange{}
	for _, portStr := range strings.Split(portsStr, ",") {
		rang := strings.Split(portStr, "-")
		switch len(rang) {
		case 1:
			port, err := strconv.ParseUint(rang[0], Base10, BitSize16)
			if err != nil {
				return nil, err
			}
			ports = append(ports, tailcfg.PortRange{
				First: uint16(port),
				Last:  uint16(port),
			})

		case expectedTokenItems:
			start, err := strconv.ParseUint(rang[0], Base10, BitSize16)
			if err != nil {
				return nil, err
			}
			last, err := strconv.ParseUint(rang[1], Base10, BitSize16)
			if err != nil {
				return nil, err
			}
			ports = append(ports, tailcfg.PortRange{
				First: uint16(start),
				Last:  uint16(last),
			})

		default:
			return nil, errInvalidPortFormat
		}
	}

	return &ports, nil
}

func filterMachinesByUser(machines []Machine, user string) []Machine {
	out := []Machine{}
	for _, machine := range machines {
		if machine.User.Name == user {
			out = append(out, machine)
		}
	}

	return out
}

// expandTagOwners will return a list of user. An owner can be either a user or a group
// a group cannot be composed of groups.
func expandTagOwners(
	aclPolicy ACLPolicy,
	tag string,
	stripEmailDomain bool,
) ([]string, error) {
	var owners []string
	ows, ok := aclPolicy.TagOwners[tag]
	if !ok {
		return []string{}, fmt.Errorf(
			"%w. %v isn't owned by a TagOwner. Please add one first. https://tailscale.com/kb/1018/acls/#tag-owners",
			errInvalidTag,
			tag,
		)
	}
	for _, owner := range ows {
		if strings.HasPrefix(owner, "group:") {
			gs, err := expandGroup(aclPolicy, owner, stripEmailDomain)
			if err != nil {
				return []string{}, err
			}
			owners = append(owners, gs...)
		} else {
			owners = append(owners, owner)
		}
	}

	return owners, nil
}

// expandGroup will return the list of user inside the group
// after some validation.
func expandGroup(
	aclPolicy ACLPolicy,
	group string,
	stripEmailDomain bool,
) ([]string, error) {
	outGroups := []string{}
	aclGroups, ok := aclPolicy.Groups[group]
	if !ok {
		return []string{}, fmt.Errorf(
			"group %v isn't registered. %w",
			group,
			errInvalidGroup,
		)
	}
	for _, name := range aclGroups {
		if strings.HasPrefix(name, "group:") {
			return []string{}, fmt.Errorf(
				"%w. A group cannot be composed of groups. https://tailscale.com/kb/1018/acls/#groups",
				errInvalidGroup,
			)
		}
		if stripEmailDomain {
			if atIdx := strings.Index(name, "@"); atIdx > 0 {
				name = name[:atIdx]
			}
		}
		outGroups = append(outGroups, name)
	}

	return outGroups, nil
}
