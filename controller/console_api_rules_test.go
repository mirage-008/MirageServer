package controller

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeACLRule(t *testing.T) {
	t.Parallel()

	rule := normalizeACLRule(ACLRuleBody{
		Action:       "  ACCEPT ",
		Protocol:     " TCP ",
		Sources:      []string{" alice ", "", " group:dev "},
		Destinations: []string{" tag:web:443 ", "  ", " internal-db:5432 "},
	})

	want := ACL{
		Action:       "accept",
		Protocol:     "tcp",
		Sources:      []string{"alice", "group:dev"},
		Destinations: []string{"tag:web:443", "internal-db:5432"},
	}
	if !reflect.DeepEqual(rule, want) {
		t.Fatalf("unexpected normalized rule: got %#v want %#v", rule, want)
	}
}

func TestValidateACLRuleShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    ACL
		wantErr string
	}{
		{
			name: "valid rule",
			rule: ACL{
				Action:       "accept",
				Protocol:     "tcp",
				Sources:      []string{"group:dev"},
				Destinations: []string{"tag:web:443"},
			},
		},
		{
			name: "missing action",
			rule: ACL{
				Sources:      []string{"group:dev"},
				Destinations: []string{"tag:web:443"},
			},
			wantErr: "规则动作不能为空",
		},
		{
			name: "invalid action",
			rule: ACL{
				Action:       "drop",
				Sources:      []string{"group:dev"},
				Destinations: []string{"tag:web:443"},
			},
			wantErr: "规则动作无效",
		},
		{
			name: "missing sources",
			rule: ACL{
				Action:       "accept",
				Destinations: []string{"tag:web:443"},
			},
			wantErr: "规则来源不能为空",
		},
		{
			name: "missing destinations",
			rule: ACL{
				Action:  "accept",
				Sources: []string{"group:dev"},
			},
			wantErr: "规则目标不能为空",
		},
		{
			name: "invalid protocol",
			rule: ACL{
				Action:       "accept",
				Protocol:     "badproto",
				Sources:      []string{"group:dev"},
				Destinations: []string{"tag:web:443"},
			},
			wantErr: "协议无效",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateACLRuleShape(tt.rule)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateACLRuleShape returned error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("unexpected error: got %v want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseACLRuleID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "valid id", raw: "3", want: 3},
		{name: "trimmed id", raw: " 12 ", want: 12},
		{name: "empty id", raw: "", wantErr: true},
		{name: "negative id", raw: "-1", wantErr: true},
		{name: "non number", raw: "abc", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseACLRuleID(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseACLRuleID returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected id: got %d want %d", got, tt.want)
			}
		})
	}
}

func TestACLRuleValidationMessage(t *testing.T) {
	t.Parallel()

	if got := aclRuleValidationMessage(errInvalidAutoGroupSelfSource); got != "autogroup:self 目标只允许来源为用户、用户组、* 或 autogroup:member" {
		t.Fatalf("unexpected autogroup:self message: %q", got)
	}

	plainErr := errors.New("plain error")
	if got := aclRuleValidationMessage(plainErr); got != plainErr.Error() {
		t.Fatalf("unexpected fallback message: got %q want %q", got, plainErr.Error())
	}
}
