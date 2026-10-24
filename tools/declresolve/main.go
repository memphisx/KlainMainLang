// Command declresolve checks status-row declaration names (lib/conform's
// ResolveDecl forms), one per line on standard input: each prints "ok
// <name>" or the reason it does not resolve.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"KlainMainLang/lib/conform"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" {
			continue
		}
		if err := conform.ResolveDecl(name); err != nil {
			fmt.Println("err", err)
			continue
		}
		fmt.Println("ok", name)
	}
}
