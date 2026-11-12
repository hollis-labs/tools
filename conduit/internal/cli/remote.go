package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// approve and reject are done by a signed-in person against a running server. The person's password is read from the
// terminal and from nowhere else (no flag, no environment variable, no file); the session lives in this process's
// memory for the one request and is signed out again. Nothing is stored. The server decides who may approve: this
// command is only a client of POST /api/runs/{id}/approve and /reject.

// isTTY and readPassword are variables so tests can stand in for a terminal.
var (
	isTTY        = func(f *os.File) bool { fd := f.Fd(); return fd <= math.MaxInt && term.IsTerminal(int(fd)) }
	readPassword = func(f *os.File) ([]byte, error) { return term.ReadPassword(int(f.Fd())) }
	// readLine reads one line from the terminal a byte at a time, so nothing beyond the line is consumed.
	readLine = func(f *os.File) (string, error) {
		var line []byte
		one := make([]byte, 1)
		for {
			n, err := f.Read(one)
			if n == 1 {
				if one[0] == '\n' {
					return strings.TrimRight(string(line), "\r"), nil
				}
				line = append(line, one[0])
			}
			if err != nil {
				return string(line), err
			}
		}
	}
)

func (c *cmd) remote(verb string, args []string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(c.err)
	server := fs.String("server", "", "base `URL` of the running conduit server (required)")
	user := fs.String("user", "", "user name to sign in as (required)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if *server == "" || *user == "" || fs.NArg() != 1 {
		return c.fail("usage: conduit %s --server URL --user NAME <run>", verb)
	}
	base, err := serverURL(*server)
	if err != nil {
		return c.fail("%v", err)
	}
	if c.in == nil || !isTTY(c.in) {
		return c.fail("refusing to %s: the password is read from a terminal only, and stdin is not a terminal", verb)
	}
	fmt.Fprintf(c.err, "Password for %s: ", *user)
	pw, err := readPassword(c.in)
	fmt.Fprintln(c.err)
	if err != nil {
		return c.fail("read password: %v", err)
	}
	defer clear(pw)

	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, body any, out any) (int, string) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(c.ctx, method, base+path, rd)
		if err != nil {
			return 0, err.Error()
		}
		req.Header.Set("X-Requested-With", "conduit-cli")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode/100 != 2 {
			return resp.StatusCode, errorText(raw)
		}
		if out != nil {
			_ = json.Unmarshal(raw, out)
		}
		return resp.StatusCode, ""
	}

	if st, msg := call("POST", "/api/login", map[string]string{"username": *user, "password": string(pw)}, nil); st != http.StatusOK {
		return c.fail("sign in failed: %s", failText(st, msg))
	}
	defer call("POST", "/api/logout", nil, nil)

	var list struct {
		Pending []struct {
			ID        string `json:"id"`
			Command   string `json:"command"`
			Cwd       string `json:"cwd"`
			Effect    string `json:"effect"`
			Principal string `json:"principal"`
			Verdict   string `json:"verdict"`
		} `json:"pending"`
	}
	if st, msg := call("GET", "/api/approvals", nil, &list); st != http.StatusOK {
		return c.fail("list approvals: %s", failText(st, msg))
	}
	want := fs.Arg(0)
	var id string
	var shown struct{ command, cwd, effect, principal string }
	for _, p := range list.Pending {
		if p.ID == want || strings.HasPrefix(p.ID, want) || strings.HasSuffix(p.ID, want) {
			if id != "" {
				return c.fail("%q matches more than one pending run", want)
			}
			id = p.ID
			shown.command, shown.cwd, shown.effect, shown.principal = p.Command, p.Cwd, p.Effect, p.Principal
		}
	}
	if id == "" {
		fmt.Fprintf(c.err, "conduit: no pending run matches %q\n", want)
		return ExitNotFound
	}
	// What the submitter wrote is shown whole, with control characters made visible: the person decides on this text.
	fmt.Fprintf(c.err, "\nrun      %s\nfrom     %s\neffect   %s\ncwd      %s\ncommand\n  │ %s\n\n",
		id, visible(shown.principal), visible(shown.effect), visible(shown.cwd), block(shown.command, "  "))
	if verb == "approve" {
		fmt.Fprint(c.err, "Type yes to approve this command: ")
		answer, err := readLine(c.in)
		if err != nil && answer == "" || strings.TrimSpace(answer) != "yes" {
			fmt.Fprintln(c.err, "\nconduit: not approved; nothing was sent")
			return ExitDenied
		}
	}
	var res struct {
		State string `json:"state"`
	}
	if st, msg := call("POST", "/api/runs/"+id+"/"+verb, nil, &res); st != http.StatusOK {
		code := ExitError
		if st == http.StatusForbidden {
			code = ExitDenied
		}
		fmt.Fprintf(c.err, "conduit: %s: %s\n", verb, failText(st, msg))
		return code
	}
	fmt.Fprintf(c.out, "%s: %s\n", id, res.State)
	return ExitOK
}

// serverURL accepts an http(s) base URL. A password is never sent in clear over anything but loopback.
func serverURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("--server must be an http(s) URL, got %q", raw)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("refusing to send a password over plain http to %q: use https, or a loopback address", host)
		}
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), nil
}

func errorText(raw []byte) string {
	var e struct {
		Error struct{ Message string } `json:"error"`
		Msg   string                   `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message
		}
		return e.Msg
	}
	return ""
}

func failText(status int, msg string) string {
	if status == 0 {
		return msg
	}
	if msg == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Sprintf("HTTP %d: %s", status, msg)
}
