package cli

import (
	"embed"
	"flag"
	"fmt"
	"strings"
)

//go:embed shell/*.sh
var shellScripts embed.FS

// defaultKey is the binding per shell, in that shell's own notation.
var defaultKey = map[string]string{"bash": `\C-r`, "zsh": `^R`}

// initShell prints the integration script for a shell, so the binary carries its
// own integration and cannot drift from the picker it binds.
func (c *cmd) initShell(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(c.err)
	k := fs.String("key", "", "key binding in the shell's notation (bash: '\\C-r', zsh: '^R')")
	// the shell name comes first: conduit init bash -key '\C-g'
	if len(args) == 0 {
		return c.fail("init: want a shell: bash or zsh")
	}
	shell, rest := args[0], args[1:]
	if err := fs.Parse(rest); err != nil {
		return ExitError
	}
	body, err := shellScripts.ReadFile("shell/" + shell + ".sh")
	if err != nil {
		return c.fail("init: unsupported shell %q (want bash or zsh)", shell)
	}
	key := *k
	if key == "" {
		key = defaultKey[shell]
	}
	if strings.ContainsAny(key, "'\"\n$`") {
		return c.fail("init: key %q has characters that are unsafe in a shell binding", key)
	}
	fmt.Fprint(c.out, strings.ReplaceAll(string(body), "@@KEY@@", key))
	return ExitOK
}
