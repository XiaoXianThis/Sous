package project

import "testing"

func TestKey(t *testing.T) {
	want := "github.com/XiaoXianThis/Sous"
	for _, in := range []string{
		"https://github.com/XiaoXianThis/Sous",
		"https://github.com/XiaoXianThis/Sous.git",
		"https://user:tok@GitHub.com/XiaoXianThis/Sous/",
		"git@github.com:XiaoXianThis/Sous.git",
		"ssh://git@github.com:22/XiaoXianThis/Sous.git",
		"github.com:XiaoXianThis/Sous",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "/srv/repos/a.git", "./a", "https://github.com/"} {
		if got := Key(in); got != "" {
			t.Errorf("Key(%q) = %q, want empty", in, got)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"sous", "my-app", "a1-b2"} {
		if !ValidName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "My-App", "a_b", "-a", "a-", "a--b", "con", "com1", "a b", "../x"} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
