package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/hollis-labs/tools/conduit/internal/fuzzy"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/libs/core/panics"
)

func (c *cmd) pick(args []string) int {
	fs := flag.NewFlagSet("pick", flag.ContinueOnError)
	fs.SetOutput(c.err)
	var (
		query   = fs.String("query", "", "initial query")
		histArg = fs.String("history", "", "also offer lines from this shell history file (bash or zsh)")
		limit   = fs.Int("n", 1000, "how many conduit commands to load")
		filter  = fs.String("filter", "", "non-interactive: print matches for this query, best first")
		doRun   = fs.Bool("run", false, "run the chosen command through conduit run instead of printing it")
	)
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	var extra []string
	if *histArg != "" {
		extra = readShellHistory(*histArg, 5000)
	}
	items, err := c.svc.History(*limit, extra)
	if err != nil {
		return c.fail("%v", err)
	}

	filterSet := false
	fs.Visit(func(f *flag.Flag) { filterSet = filterSet || f.Name == "filter" })
	if filterSet {
		texts := make([]string, len(items))
		for i, it := range items {
			texts[i] = it.Command
		}
		for _, r := range fuzzy.Rank(*filter, texts) {
			fmt.Fprintln(c.out, items[r.Index].Command)
		}
		return ExitOK
	}

	chosen, ok, err := c.interactive(newPickerFor(items, *query))
	if err != nil {
		return c.fail("pick: %v", err)
	}
	if !ok {
		return ExitError
	}
	if *doRun {
		return c.run([]string{"-c", chosen})
	}
	fmt.Fprintln(c.out, chosen)
	return ExitOK
}

func newPickerFor(items []service.HistoryItem, query string) *picker {
	return newPicker(items, query, 10)
}

// interactive runs the picker on /dev/tty so it works under a shell binding
// where stdin and stdout are not the terminal. ok is false on cancel.
func (c *cmd) interactive(p *picker) (string, bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", false, fmt.Errorf("no terminal (use -filter for non-interactive use)")
	}
	defer func() { _ = tty.Close() }()
	fd := int(tty.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = term.Restore(fd, old) }()

	width, rows, err := term.GetSize(fd)
	if err != nil {
		width, rows = 80, 24
	}
	p.height = max(min(p.height, rows-3), 3)

	// Reserve the region below the cursor, then redraw in place.
	region := 1 + p.height + 1
	fmt.Fprint(tty, strings.Repeat("\n", region), "\x1b[", region, "A")
	draw := func() {
		lines := p.view(width, true)
		fmt.Fprint(tty, "\r\x1b[J")
		for i, l := range lines {
			if i > 0 {
				fmt.Fprint(tty, "\r\n")
			}
			fmt.Fprint(tty, l)
		}
		if up := len(lines) - 1; up > 0 {
			fmt.Fprintf(tty, "\x1b[%dA", up)
		}
		fmt.Fprintf(tty, "\r\x1b[%dC", 2+len([]rune(string(p.query))))
	}
	defer fmt.Fprint(tty, "\r\x1b[J")

	keys := readKeys(tty)
	draw()
	for k := range keys {
		switch p.key(k) {
		case actAccept:
			return p.selected(), true, nil
		case actCancel:
			return "", false, nil
		case actNone:
		}
		draw()
	}
	return "", false, nil
}

// readKeys decodes terminal input into keys. A lone ESC is a cancel; arrow and
// paging sequences are recognized; other control bytes are ignored.
func readKeys(r io.Reader) <-chan key {
	out := make(chan key)
	// Both goroutines are contained: a panic closes the channel (the picker then
	// cancels and restores the terminal) instead of killing the process mid-draw.
	panics.Go("picker keys", func() {
		defer close(out)
		br := bufio.NewReader(r)
		bytes := make(chan byte)
		panics.Go("picker reader", func() {
			defer close(bytes)
			for {
				b, err := br.ReadByte()
				if err != nil {
					return
				}
				bytes <- b
			}
		}, nil)
		for b := range bytes {
			switch {
			case b == 0x1b:
				select {
				case n, ok := <-bytes:
					if !ok {
						out <- key{kind: kCancel}
						return
					}
					if n != '[' && n != 'O' {
						out <- key{kind: kCancel}
						continue
					}
					c, ok := <-bytes
					if !ok {
						return
					}
					switch c {
					case 'A':
						out <- key{kind: kUp}
					case 'B':
						out <- key{kind: kDown}
					case '5', '6': // PgUp, PgDn: ESC [ 5 ~
						<-bytes
						if c == '5' {
							out <- key{kind: kPgUp}
						} else {
							out <- key{kind: kPgDn}
						}
					case '3': // Delete: ESC [ 3 ~
						<-bytes
					}
				case <-time.After(60 * time.Millisecond):
					out <- key{kind: kCancel}
				}
			case b == '\r' || b == '\n':
				out <- key{kind: kEnter}
			case b == 0x7f || b == 0x08:
				out <- key{kind: kBackspace}
			case b == 0x15:
				out <- key{kind: kClear}
			case b == 0x17:
				out <- key{kind: kDelWord}
			case b == 0x10:
				out <- key{kind: kUp}
			case b == 0x0e:
				out <- key{kind: kDown}
			case b == 0x03 || b == 0x04 || b == 0x07:
				out <- key{kind: kCancel}
			case b >= 0x20 && b < 0x7f:
				out <- key{kind: kRune, r: rune(b)}
			case b >= 0xc0: // start of a UTF-8 sequence
				n := 1
				switch {
				case b >= 0xf0:
					n = 3
				case b >= 0xe0:
					n = 2
				}
				buf := []byte{b}
				for i := 0; i < n; i++ {
					nb, ok := <-bytes
					if !ok {
						return
					}
					buf = append(buf, nb)
				}
				if r := []rune(string(buf)); len(r) == 1 {
					out <- key{kind: kRune, r: r[0]}
				}
			}
		}
	}, nil)
	return out
}

// readShellHistory returns up to n commands from a bash or zsh history file,
// newest first, without duplicates. Unreadable files give no history rather
// than an error: the picker still works from conduit's own runs.
func readShellHistory(path string, n int) []string {
	b, err := os.ReadFile(path) //nolint:gosec // the operator names their own history file
	if err != nil {
		return nil
	}
	var cmds []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			cmds = append(cmds, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case cur.Len() > 0 && strings.HasSuffix(cur.String(), "\\"): // zsh multi-line continuation
			s := strings.TrimSuffix(cur.String(), "\\")
			cur.Reset()
			cur.WriteString(s + "\n" + line)
		case strings.HasPrefix(line, ": ") && strings.Contains(line, ";"): // zsh extended
			flush()
			cur.WriteString(line[strings.Index(line, ";")+1:])
		case strings.HasPrefix(line, "#") && len(line) > 1 && line[1] >= '0' && line[1] <= '9': // bash timestamp
			flush()
		default:
			flush()
			cur.WriteString(line)
		}
	}
	flush()
	seen := map[string]bool{}
	var out []string
	for i := len(cmds) - 1; i >= 0 && len(out) < n; i-- {
		if !seen[cmds[i]] {
			seen[cmds[i]] = true
			out = append(out, cmds[i])
		}
	}
	return out
}
