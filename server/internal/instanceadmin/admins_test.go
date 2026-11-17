package instanceadmin

import "testing"

func TestAdminsRequiresConfiguration(t *testing.T) {
	for _, emails := range [][]string{nil, {}, {"", "   "}} {
		admins := NewAdmins(emails)
		if admins.Configured() {
			t.Fatalf("NewAdmins(%q).Configured() = true, want false", emails)
		}
		if admins.Allows("ops@example.com") {
			t.Fatalf("NewAdmins(%q).Allows(ops@example.com) = true with no configured admin", emails)
		}
	}
}

func TestAdminsMatchesCaseAndWhitespaceInsensitive(t *testing.T) {
	admins := NewAdmins([]string{"  Ops@Example.COM ", "", "platform-lead@example.com"})
	if !admins.Configured() {
		t.Fatal("Configured() = false, want true")
	}
	for _, email := range []string{
		"ops@example.com",
		"OPS@EXAMPLE.COM",
		" Ops@Example.com\t",
		"platform-lead@example.com",
	} {
		if !admins.Allows(email) {
			t.Errorf("Allows(%q) = false, want true", email)
		}
	}
}

func TestAdminsRejectsUnlistedAndEmptyEmails(t *testing.T) {
	admins := NewAdmins([]string{"ops@example.com"})
	for _, email := range []string{"", "   ", "someone@example.com", "ops@example.com.evil.test", "ops@example.co"} {
		if admins.Allows(email) {
			t.Errorf("Allows(%q) = true, want false", email)
		}
	}
}

func TestNewAdminsCannotBeBypassedByEmptyEntry(t *testing.T) {
	// A trailing comma must not admit the empty string: that would make every
	// account without an email an administrator.
	admins := NewAdmins([]string{"ops@example.com", ""})
	if admins.Allows("") {
		t.Fatal("Allows(\"\") = true; an empty entry must not grant access")
	}
}

func TestAdminsTreatsDuplicateEntriesAsOne(t *testing.T) {
	// Operators repeat an address when a list is edited by hand; it must not
	// change who is admitted or make Configured() depend on the count.
	admins := NewAdmins([]string{"Ops@Example.com", "ops@example.com", "  OPS@example.com"})
	if !admins.Allows("ops@example.com") {
		t.Fatal("Allows(ops@example.com) = false with three spellings of the same address")
	}
	if admins.Allows("platform-lead@example.com") {
		t.Fatal("Allows(platform-lead@example.com) = true for a list that never named it")
	}
}
