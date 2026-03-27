package controller

import "testing"

func TestACLMetaDataDefaults(t *testing.T) {
	t.Parallel()

	data := ACLMetaData{
		Sections:         []string{"policy", "rules", "auto-approvers", "tags", "groups", "hosts"},
		SSHEnabled:       false,
		TestsImplemented: false,
	}

	if len(data.Sections) == 0 {
		t.Fatal("expected sections to be present")
	}
	if data.TestsImplemented {
		t.Fatal("tests should remain unimplemented")
	}
}
