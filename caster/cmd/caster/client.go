package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/tools/caster/internal/cli"
	"github.com/hollis-labs/tools/caster/internal/client"
	"github.com/hollis-labs/tools/caster/internal/service"
	"github.com/hollis-labs/tools/caster/internal/settings"
)

// serviceFor returns the service a data command or the stdio server calls, and the context to call it
// with. This machine is either a client of a home node (every operation is an HTTP call that carries
// the API key; the home node decides what the key may do) or it works on the local store as the local
// principal (read, resolve, write, observe: never act or admin). The returned function releases what
// the service holds (the Tesseract gateway child).
func (c *caster) serviceFor(ctx context.Context) (*service.Service, context.Context, func(), error) {
	if c.profile != nil {
		cl, err := client.New(c.profile.URL, c.profile.Source(), c.profile.AllowHTTP)
		if err != nil {
			return nil, ctx, nil, err
		}
		return cl.Service(), ctx, func() {}, nil
	}
	cfg, err := settings.Parse(c.app.Config[settings.Key])
	if err != nil {
		return nil, ctx, nil, err
	}
	svc, closeGW, err := newService(c.app, cfg)
	if err != nil {
		return nil, ctx, nil, err
	}
	return svc, service.WithPrincipal(ctx, service.LocalPrincipal()), closeGW, nil
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// dataCmds are the note commands. Each takes the old CLI's flags and arguments verbatim and exits with
// the helper-protocol codes (0 ok, 2 not found, 3 denied, 1 error, 64 bad input), because Conduit and
// other callers use them as a resolver; cobra only registers them.
func (c *caster) dataCmds(ctx context.Context) []*cobra.Command {
	var out []*cobra.Command
	for _, verb := range dataVerbs {
		out = append(out, &cobra.Command{
			Use:                verb,
			Short:              "caster " + verb + " (see `caster help`)",
			DisableFlagParsing: true,
			SilenceUsage:       true,
			RunE: func(_ *cobra.Command, args []string) error {
				if hasHelp(args) || c.kind != kindData {
					_, _ = fmt.Fprint(c.out, cli.Usage())
					return nil
				}
				svc, callCtx, release, err := c.serviceFor(ctx)
				if err != nil {
					return err
				}
				defer release()
				if code := cli.Run(callCtx, svc, append([]string{verb}, args...), c.in, c.out, c.errw); code != cli.ExitOK {
					return exitStatus(code)
				}
				return nil
			},
		})
	}
	return out
}

// loginCmd pairs this machine with a home node: it redeems an identity pairing code (an administrator
// mints it with POST /api/identity/pairing-codes) for an API key and stores the key under a secrets
// reference, plus a profile that points at it.
//
//	caster login [-name home] [-ref keychain://caster/home] [-allow-http] <url> <code>
func (c *caster) loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "login <url> <code>",
		Short:              "Pair this machine with a home node using an identity pairing code",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if hasHelp(args) || c.kind != kindClient {
				return cmd.Help()
			}
			return c.login(cmd.Context(), args)
		},
	}
}

func (c *caster) login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(c.errw)
	name := fs.String("name", "home", "name for the stored credential")
	ref := fs.String("ref", "", "secrets reference to store the key under (default: keychain://caster/<name>, else file://caster/<name>)")
	allowHTTP := fs.Bool("allow-http", os.Getenv("CASTER_ALLOW_HTTP") == "1", "accept plain http to a non-loopback, non-tailnet host")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return exitStatus(cli.ExitUsage) // the flag package already printed the problem
		}
		args = fs.Args()
		if len(args) > 0 {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	if len(pos) != 2 {
		_, _ = fmt.Fprintln(c.errw, "usage: caster login [-name home] [-ref secrets-ref] [-allow-http] <url> <code>\n"+
			"  an administrator of the home node mints the code: POST /api/identity/pairing-codes")
		return exitStatus(cli.ExitUsage)
	}
	url, code := pos[0], pos[1]
	cl, err := client.New(url, nil, *allowHTTP) // redeeming a code needs no credential
	if err != nil {
		return err
	}
	paired, err := cl.Pair(ctx, code)
	if err != nil {
		if service.KindOf(err) == service.KindUnauthenticated {
			_, _ = fmt.Fprintf(c.errw, "caster: %v\n  A code works once and expires; every kind of mistake (typo, truncation, reuse, expiry) gets this same answer on purpose.\n", err)
			return exitStatus(cli.ExitDenied)
		}
		return err
	}
	stored, err := client.StoreToken(ctx, *name, *ref, paired.Key)
	if err != nil {
		return fmt.Errorf("paired as %s, but the key could not be stored (%w); revoke that key on the home node and retry with -ref file://caster/%s", paired.Credential.ID, err, *name)
	}
	if got, err := client.SecretsTokenSource(stored)(ctx); err != nil || got != paired.Key {
		return fmt.Errorf("paired as %s, but the stored key does not read back from %s; revoke that key on the home node and retry", paired.Credential.ID, stored)
	}
	if err := client.SaveProfile(c.layout.ConfigDir(), client.Profile{URL: strings.TrimRight(url, "/"), TokenRef: stored,
		KeyID: paired.Credential.ID, Scopes: paired.Credential.Scopes, AllowHTTP: *allowHTTP}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.out, "paired with %s as %s\nscopes  %s\nkey     stored at %s\n\nThis machine now uses the home node. `caster whoami` confirms; CASTER_LOCAL=1 uses a local store instead.\n",
		cl.BaseURL(), paired.Credential.ID, strings.Join(paired.Credential.Scopes, ", "), stored)
	return nil
}

func (c *caster) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Forget the home node on this machine",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.kind != kindClient {
				return cmd.Help()
			}
			p, err := client.LoadProfile(c.layout.ConfigDir())
			if err != nil {
				return err
			}
			if p == nil {
				_, _ = fmt.Fprintln(c.out, "not logged in")
				return nil
			}
			if p.TokenRef != "" {
				if err := client.DeleteToken(cmd.Context(), p.TokenRef); err != nil {
					_, _ = fmt.Fprintf(c.errw, "warning: could not delete the stored key (%v)\n", err)
				}
			}
			if err := client.RemoveProfile(c.layout.ConfigDir()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(c.out, "forgot %s on this machine\n", p.URL)
			if p.KeyID != "" {
				_, _ = fmt.Fprintf(c.out, "the key %s still exists there; an administrator ends it with DELETE /api/identity/keys/%s\n", p.KeyID, p.KeyID)
			}
			return nil
		},
	}
}

// remoteCmd says which store this machine uses and, as a client, whether the home node answers and
// what it thinks of this key.
func (c *caster) remoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "remote",
		Short:        "Show which store this machine uses and the home node's health",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.kind != kindClient {
				return cmd.Help()
			}
			ctx := cmd.Context()
			p, err := client.Active(c.layout.ConfigDir(), os.Getenv)
			if err != nil {
				return err
			}
			if p == nil {
				_, _ = fmt.Fprintln(c.out, "mode: local store (not paired with a home node; `caster login <url> <code>` to pair)")
				return nil
			}
			_, _ = fmt.Fprintf(c.out, "mode:    remote\nhome:    %s\nkey:     %s\n", p.URL, p.TokenLabel())
			cl, err := client.New(p.URL, p.Source(), p.AllowHTTP)
			if err != nil {
				return err
			}
			status, err := cl.Health(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(c.out, "health:  unreachable (%v)\n", err)
				return exitStatus(cli.ExitError)
			}
			_, _ = fmt.Fprintf(c.out, "health:  %s\n", status)
			w, err := cl.Service().Identity.Whoami(ctx, struct{}{})
			if err != nil {
				_, _ = fmt.Fprintf(c.out, "whoami:  %v\n", err)
				return exitStatus(cli.ExitDenied)
			}
			_, _ = fmt.Fprintf(c.out, "whoami:  %s (%s)\nscopes:  %s\n", w.ID, w.Kind, strings.Join(w.Scopes, ", "))
			return nil
		},
	}
}

// pathsCmd says where data lives, without creating any of it.
func (c *caster) pathsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "paths",
		Short:        "Show where data lives",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.kind != kindClient {
				return cmd.Help()
			}
			p, err := client.Active(c.layout.ConfigDir(), os.Getenv)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if p != nil {
				_, _ = fmt.Fprintf(c.out, "remote  %s\nkey     %s\nprofile %s\n", p.URL, p.TokenLabel(), client.ProfilePath(c.layout.ConfigDir()))
				return nil
			}
			db := databasePath(c.app)
			_, _ = fmt.Fprintf(c.out, "data    %s\ndb      %s\nblobs   %s\nconfig  %s\n", c.layout.DataDir(), db, storageRoot(db), c.layout.ConfigDir())
			return nil
		},
	}
}
