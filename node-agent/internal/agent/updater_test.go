package agent

import "testing"

func TestReleaseVersionValidation(t *testing.T) {
	valid := []string{"0.1.0-beta.3", "0.1.0-beta.3.3", "1.0.0", "2.4.1-rc.2"}
	invalid := []string{"", "v0.1.0", "0.1", "0.1.0/beta", "0.1.0 beta", "../../tmp"}
	for _, version := range valid {
		if !releaseVersionPattern.MatchString(version) { t.Errorf("valid version %q was rejected", version) }
	}
	for _, version := range invalid {
		if releaseVersionPattern.MatchString(version) { t.Errorf("invalid version %q was accepted", version) }
	}
}
