package plugin

import "testing"

func TestSplitNPMSpec(t *testing.T) {
	cases := []struct {
		spec, name, version string
	}{
		{"@cend/octopus-plugin-workbuddy-auth@1.0.0", "@cend/octopus-plugin-workbuddy-auth", "1.0.0"},
		{"@cend/octopus-plugin-workbuddy-auth", "@cend/octopus-plugin-workbuddy-auth", ""},
		{"lodash@4.17.21", "lodash", "4.17.21"},
		{"lodash", "lodash", ""},
	}
	for _, tc := range cases {
		name, version := splitNPMSpec(tc.spec)
		if name != tc.name || version != tc.version {
			t.Errorf("splitNPMSpec(%q) = (%q, %q), want (%q, %q)", tc.spec, name, version, tc.name, tc.version)
		}
	}
}

func TestSafePluginDirName(t *testing.T) {
	if got := safePluginDirName("@scope/name"); got != "scope__name" {
		t.Fatalf("safePluginDirName = %q", got)
	}
}
