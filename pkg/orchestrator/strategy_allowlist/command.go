// Package strategy_allowlist shows and changes STRATEGY_ALLOWLIST, the line in the config
// object (gs://<bucket>/config/env) that says which strategies the jobs may scan, stage
// and trade. deploy_gae/config.sh set does the same from a shell; this is the Go form so
// it is one `orchestrator` subcommand.
package strategy_allowlist

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
)

// PrevObject is where the config object is kept before every change.
const PrevObject = "config/env.prev"

// CommandOptions are the inputs to the `orchestrator strategy_allowlist` job.
type CommandOptions struct {
	Action string // show (default), set, add or remove
	Args   []string
	DryRun bool // report the new list and write nothing
}

// Apply returns the list that results from action on current, without touching storage.
// set replaces it with the comma-separated list in args; add appends ids not already
// present; remove drops ids (an id that is not there is an error).
func Apply(current []string, action string, args []string) ([]string, error) {
	switch action {
	case "set":
		if len(args) == 0 {
			return nil, fmt.Errorf("set needs the new list: strategy_allowlist set \"a+b,c\"")
		}
		return strategies.Parse(strings.Join(args, ","))
	case "add":
		if len(args) == 0 {
			return nil, fmt.Errorf("add needs one or more strategy ids")
		}
		add, err := strategies.Parse(strings.Join(args, ","))
		if err != nil {
			return nil, err
		}
		out := slices.Clone(current)
		for _, id := range add {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		return out, nil
	case "remove":
		if len(args) == 0 {
			return nil, fmt.Errorf("remove needs one or more strategy ids")
		}
		drop, err := strategies.Parse(strings.Join(args, ","))
		if err != nil {
			return nil, err
		}
		out := slices.Clone(current)
		for _, id := range drop {
			i := slices.Index(out, id)
			if i < 0 {
				return nil, fmt.Errorf("%q is not in the allowlist (%s)", id, strings.Join(current, ","))
			}
			out = slices.Delete(out, i, i+1)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("that would empty the allowlist: every job refuses to run without one")
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown action %q: use show, set, add or remove", action)
}

// SetLine returns data with the STRATEGY_ALLOWLIST line replaced by value (or appended
// when there is none). Every other line is kept as it is.
func SetLine(data []byte, value string) []byte {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	line := strategies.EnvAllowlist + "=" + value
	done := false
	for i, l := range lines {
		k, _, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && strings.TrimSpace(k) == strategies.EnvAllowlist {
			lines[i] = line
			done = true
		}
	}
	if !done {
		lines = append(lines, line)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// Change is the outcome of Update: the list stored before, the list after the action, and
// whether the object was written (false on a dry run, or when nothing changed).
type Change struct {
	Before  []string `json:"before"`
	After   []string `json:"after"`
	Written bool     `json:"written"`
}

// Current reads the stored list from the config object, not the process environment, so a
// shell override cannot hide what is stored.
func Current(ctx context.Context) ([]string, error) {
	_, current, err := read(ctx)
	return current, err
}

func read(ctx context.Context) (data []byte, current []string, err error) {
	bucket := config.Bucket()
	data, err = tokensync.GetObject(ctx, bucket, config.ConfigObject)
	if err != nil {
		return nil, nil, fmt.Errorf("read gs://%s/%s: %w", bucket, config.ConfigObject, err)
	}
	_, values := config.ParseEnv(data)
	current, _ = strategies.Parse(values[strategies.EnvAllowlist])
	return data, current, nil
}

// Update applies action to the stored list, checks that every entry (stack members included)
// resolves to a registered strategy, because one that does not stops every job, and, unless
// dryRun, saves the old object to config/env.prev and writes the new one. This is the one
// implementation behind the CLI and the /allowlist page. A running instance keeps the list it
// started with: redeploy, or let the idle instance stop, to pick up the change.
func Update(ctx context.Context, action string, args []string, dryRun bool) (Change, error) {
	bucket := config.Bucket()
	data, current, err := read(ctx)
	if err != nil {
		return Change{}, err
	}
	ch := Change{Before: current, After: current}
	next, err := Apply(current, action, args)
	if err != nil {
		return ch, err
	}
	if _, err := strategies.ResolveGreenlit(nil, next); err != nil {
		return ch, fmt.Errorf("not written, the new list does not resolve: %w", err)
	}
	ch.After = next
	if slices.Equal(current, next) || dryRun {
		return ch, nil
	}
	if err := tokensync.PutObject(ctx, bucket, PrevObject, data, "text/plain"); err != nil {
		return ch, fmt.Errorf("save the old object to gs://%s/%s: %w", bucket, PrevObject, err)
	}
	if err := tokensync.PutObject(ctx, bucket, config.ConfigObject, SetLine(data, strings.Join(next, ",")), "text/plain"); err != nil {
		return ch, fmt.Errorf("write gs://%s/%s: %w", bucket, config.ConfigObject, err)
	}
	ch.Written = true
	return ch, nil
}

// RunCommand is the `orchestrator strategy_allowlist` job.
func RunCommand(ctx context.Context, out io.Writer, opts CommandOptions) error {
	action := opts.Action
	if action == "" {
		action = "show"
	}
	fmt.Fprintf(out, "\n%s%s=== strategy_allowlist ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	if action == "show" {
		current, err := Current(ctx)
		if err != nil {
			return err
		}
		printList(out, "STRATEGY_ALLOWLIST", current)
		return nil
	}
	jobenv.WriteMode(out, opts.DryRun)
	ch, err := Update(ctx, action, opts.Args, opts.DryRun)
	if err != nil {
		return err
	}
	printList(out, "before", ch.Before)
	printList(out, "after ", ch.After)
	if slices.Equal(ch.Before, ch.After) {
		fmt.Fprintln(out, "no change")
		return nil
	}
	if ch.Written {
		bucket := config.Bucket()
		fmt.Fprintf(out, "written to gs://%s/%s (the old one is gs://%s/%s). Redeploy for a running instance to pick it up.\n", bucket, config.ConfigObject, bucket, PrevObject)
	}
	return nil
}

func printList(out io.Writer, label string, list []string) {
	fmt.Fprintf(out, "%s:\n", label)
	if len(list) == 0 {
		fmt.Fprintln(out, "  (empty)")
	}
	for _, id := range list {
		fmt.Fprintf(out, "  %s\n", id)
	}
}
