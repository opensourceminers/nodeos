package update

import "testing"

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.3.0", "0.2.0", true},
		{"0.2.1", "0.2.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"v0.3.0", "0.2.0", true},
		{"0.2", "0.2.0", false},
		{"0.2.0.1", "0.2.0", true},
		{"", "0.2.0", false},
	}
	for _, c := range cases {
		if got := VersionNewer(c.a, c.b); got != c.want {
			t.Errorf("VersionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
