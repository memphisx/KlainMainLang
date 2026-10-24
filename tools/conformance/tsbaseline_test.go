package main

import "testing"

func TestTSBaselineName(t *testing.T) {
	cases := []struct{ src, want string }{
		{"// @strict: true\nlet x = 1\n", "c"},
		{"//@target: ES5, ES2015\nlet x\n", "c(target=es2015)"},
		{"// @moduleResolution: classic, node16, nodenext\n// @allowImportingTsExtensions: true,false\n", "c(allowimportingtsextensions=true,moduleresolution=nodenext)"},
		{"// @target: es5, esnext\n// @module: commonjs, esnext\n", "c(module=esnext,target=esnext)"},
	}
	for _, c := range cases {
		if got := tsBaselineName("c", c.src); got != c.want {
			t.Errorf("tsBaselineName(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
