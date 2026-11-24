package skillpolicy

import "testing"

func TestNormalize(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"all_enabled":     AllEnabled,
		" all_disabled ":  AllDisabled,
		"FOLLOW_EXTERNAL": FollowExternal,
		"":                Default,
		"made_up":         Default,
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValid(t *testing.T) {
	t.Parallel()

	for _, value := range []string{AllEnabled, AllDisabled, FollowExternal, " FOLLOW_EXTERNAL "} {
		if !Valid(value) {
			t.Fatalf("Valid(%q) = false", value)
		}
	}
	for _, value := range []string{"", "all", "follow", "all_enabled_v2"} {
		if Valid(value) {
			t.Fatalf("Valid(%q) = true, want false", value)
		}
	}
}
