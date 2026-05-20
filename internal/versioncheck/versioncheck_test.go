package versioncheck

import "testing"

func TestSemverLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.0.36", "v0.0.38", true},
		{"v0.0.38", "v0.0.38", false},
		{"v0.0.38", "v0.0.36", false},
		{"v0.1.0", "v0.0.99", false},
		{"v0.0.99", "v0.1.0", true},
		{"v1.0.0", "v0.99.99", false},
		{"v0.99.99", "v1.0.0", true},
		{"v0.0.38", "v0.0.38-rc1", false}, // pre-release stripped → equal
		{"garbage", "v0.0.38", false},      // parse fail → false
		{"v0.0.38", "garbage", false},      // parse fail → false
	}
	for _, c := range cases {
		got := semverLess(c.a, c.b)
		if got != c.want {
			t.Errorf("semverLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateAvailable_NoCache(t *testing.T) {
	c := &Checker{}
	if c.UpdateAvailable("v0.0.36") {
		t.Error("with no cache, should not claim update")
	}
}

func TestUpdateAvailable_DevVersion(t *testing.T) {
	c := &Checker{latest: "v0.0.38"}
	if c.UpdateAvailable("dev") {
		t.Error("dev version should never claim update")
	}
}

func TestUpdateAvailable_Newer(t *testing.T) {
	c := &Checker{latest: "v0.0.38"}
	if !c.UpdateAvailable("v0.0.36") {
		t.Error("v0.0.36 should be < v0.0.38")
	}
}

func TestUpdateAvailable_Same(t *testing.T) {
	c := &Checker{latest: "v0.0.38"}
	if c.UpdateAvailable("v0.0.38") {
		t.Error("same version should not claim update")
	}
}
