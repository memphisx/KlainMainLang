package ast

import "testing"

func TestModuleMangle(t *testing.T) {
	lib := ModuleMangle("URL", 4, "node_kml_url")
	user := ModuleMangle("Point", 3, "")
	for _, c := range []struct{ in, want string }{
		{"'" + lib + "_static_parse' and " + user + ".", "'URL_static_parse' and Point."},
		{user, "Point"},
	} {
		if got := UnmangleText(c.in); got != c.want {
			t.Errorf("UnmangleText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if Unmangle(lib) != "URL" || Unmangle(user) != "Point" || Unmangle("plain") != "plain" {
		t.Errorf("Unmangle: %q %q", Unmangle(lib), Unmangle(user))
	}
}

func TestIsLibraryName(t *testing.T) {
	lib := ModuleMangle("Map", 0, "node_net")
	for name, want := range map[string]bool{
		lib:                      true,
		lib + "_static_x":        true,
		ModuleMangle("P", 2, ""): false,
		"Box_" + lib + "_" + ModuleMangle("P", 2, ""): false,
		"plain": false,
	} {
		if got := IsLibraryName(name); got != want {
			t.Errorf("IsLibraryName(%q) = %v", name, got)
		}
	}
}
