package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/caster/internal/cli"
	"github.com/hollis-labs/caster/internal/client"
	"github.com/hollis-labs/caster/internal/service"
)

// login redeems a pairing code from the home node and saves the resulting
// device token under a secrets reference, plus a profile that points at it.
//
//	caster login [-name home] [-ref keychain://caster/home] [-allow-http] <url> <code>
func login(ctx context.Context, configDir string, args []string) (int, error) {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	name := fs.String("name", "home", "name for the stored credential")
	ref := fs.String("ref", "", "secrets reference to store the token under (default: keychain://caster/<name>, else file://caster/<name>)")
	allowHTTP := fs.Bool("allow-http", os.Getenv("CASTER_ALLOW_HTTP") == "1", "accept plain http to a non-loopback, non-tailnet host")
	label := fs.String("label", "", "device label shown in `caster devices` (default: the label given when the code was created)")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return cli.ExitUsage, nil
		}
		args = fs.Args()
		if len(args) > 0 {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	if len(pos) != 2 {
		fmt.Fprintln(os.Stderr, "usage: caster login [-name home] [-ref secrets-ref] [-allow-http] <url> <code>\n"+
			"  get a code on the home node with: caster pair -scope read,resolve,write -label <this machine>")
		return cli.ExitUsage, nil
	}
	url, code := pos[0], pos[1]
	c, err := client.New(url, nil, *allowHTTP) // the exchange route needs no credential
	if err != nil {
		return cli.ExitUsage, err
	}
	ex, err := c.Service().Access.Exchange(ctx, service.ExchangeRequest{Code: code, Label: *label})
	if err != nil {
		if service.KindOf(err) == service.KindUnauthenticated {
			return cli.ExitDenied, fmt.Errorf("%w\n  A code works once, lasts 5 minutes by default, and must be exact: %d characters here, starting ccg_.\n  Every kind of mistake (typo, stray space, truncation, reuse, expiry) gets this same answer on purpose.\n  Avoid copy/paste by pairing in one step:\n    caster login %s \"$(ssh <home> '/path/to/caster pair -scope read,write,resolve -label <this machine> 2>/dev/null')\"", err, len(code), url)
		}
		return cli.ExitDenied, err
	}
	stored, err := client.StoreToken(ctx, *name, *ref, ex.Token)
	if err != nil {
		return cli.ExitError, fmt.Errorf("paired as %s, but the token could not be stored (%w); revoke that device on the home node and retry with -ref file://caster/%s", ex.DeviceID, err, *name)
	}
	if got, err := client.SecretsTokenSource(stored)(ctx); err != nil || got != ex.Token {
		return cli.ExitError, fmt.Errorf("paired as %s, but the stored token does not read back from %s; revoke that device on the home node and retry", ex.DeviceID, stored)
	}
	if err := client.SaveProfile(configDir, client.Profile{URL: strings.TrimRight(url, "/"), TokenRef: stored, DeviceID: ex.DeviceID,
		Scopes: ex.Scopes, ExpiresAt: ex.ExpiresAt, AllowHTTP: *allowHTTP}); err != nil {
		return cli.ExitError, err
	}
	fmt.Printf("paired with %s as %s\nscopes  %s\nexpires %s\ntoken   %s\n", c.BaseURL(), ex.DeviceID, strings.Join(ex.Scopes, ", "), ex.ExpiresAt.Local().Format("2006-01-02"), stored)
	fmt.Println("\nThis machine now uses the home node. `caster whoami` confirms; CASTER_LOCAL=1 uses a local store instead.")
	return cli.ExitOK, nil
}

// logout forgets the home node on this machine. It cannot revoke the device
// there: that is an admin act on the home node (`caster revoke <dev_…>`).
func logout(ctx context.Context, configDir string) (int, error) {
	p, err := client.LoadProfile(configDir)
	if err != nil {
		return cli.ExitError, err
	}
	if p == nil {
		fmt.Println("not logged in")
		return cli.ExitOK, nil
	}
	if err := client.DeleteToken(ctx, p.TokenRef); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not delete the stored token (%v)\n", err)
	}
	if err := client.RemoveProfile(configDir); err != nil {
		return cli.ExitError, err
	}
	fmt.Printf("forgot %s on this machine\n", p.URL)
	if p.DeviceID != "" {
		fmt.Printf("the device still exists there; end it with: caster revoke %s (on the home node)\n", p.DeviceID)
	}
	return cli.ExitOK, nil
}

// remoteStatus says which store this machine uses and, in remote mode, whether
// the home node is reachable and what it thinks of this device.
func remoteStatus(ctx context.Context, configDir string) (int, error) {
	p, err := client.Active(configDir, os.Getenv)
	if err != nil {
		return cli.ExitError, err
	}
	if p == nil {
		fmt.Println("mode: local store (not paired with a home node; `caster login <url> <code>` to pair)")
		return cli.ExitOK, nil
	}
	fmt.Printf("mode:    remote\nhome:    %s\ntoken:   %s\n", p.URL, p.TokenRef)
	c, err := client.New(p.URL, client.SecretsTokenSource(p.TokenRef), p.AllowHTTP)
	if err != nil {
		return cli.ExitError, err
	}
	mode, err := c.Health(ctx)
	if err != nil {
		fmt.Printf("health:  unreachable (%v)\n", err)
		return cli.ExitError, nil
	}
	fmt.Printf("health:  ok (%s)\n", mode)
	w, err := c.Service().Access.Whoami(ctx, struct{}{})
	if err != nil {
		fmt.Printf("whoami:  %v\n", err)
		return cli.ExitDenied, nil
	}
	fmt.Printf("whoami:  %s (%s)\nscopes:  %s\n", w.ID, w.Kind, strings.Join(w.Scopes, ", "))
	if w.ExpiresAt != nil {
		left := time.Until(*w.ExpiresAt).Round(time.Hour)
		fmt.Printf("expires: %s (%s left)\n", w.ExpiresAt.Local().Format("2006-01-02"), left)
		if left < 7*24*time.Hour {
			fmt.Println("         renew soon: caster renew")
		}
	}
	return cli.ExitOK, nil
}
