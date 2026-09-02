package domain

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"0.1.0-beta.3.3", "0.1.0-beta.3.2", 1},
		{"0.1.0-beta.3.1", "0.1.0-beta.3.3", -1},
		{"0.1.0", "0.1.0-beta.3.3", 1},
		{"v0.1.0-beta.3.3", "0.1.0-beta.3.3", 0},
	}
	for _, test := range tests {
		if got := CompareVersions(test.left, test.right); got != test.want {
			t.Errorf("CompareVersions(%q,%q)=%d, want %d", test.left, test.right, got, test.want)
		}
	}
}
