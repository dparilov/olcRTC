package fulltunnel

import (
	"context"
	"fmt"
	"strings"
)

// CommandRunner abstracts Windows command invocation so route control can be
// exercised in tests without mutating the host.
type CommandRunner interface {
	Run(context.Context, CommandExecution) (CommandResult, error)
}

// CommandExecution describes a single apply or rollback step.
type CommandExecution struct {
	Phase        CommandPhase
	Spec         CommandSpec
	Operation    RouteOperationKind
	Target       string
	Family       string
	Interface    string
	Captured     map[string]string
	OperationIdx int
}

type dryRunCommandRunner struct {
	log Logger
}

func (r dryRunCommandRunner) Run(ctx context.Context, exec CommandExecution) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}

	if r.log != nil {
		r.log.Printf(
			"full-tunnel: dry-run %s op=%q target=%q interface=%q exec=%q args=%q",
			exec.Phase,
			exec.Operation,
			exec.Target,
			exec.Interface,
			exec.Spec.Executable,
			strings.Join(exec.Spec.Args, " "),
		)
	}

	return CommandResult{
		ExitCode: 0,
		Stdout:   fmt.Sprintf("dry-run %s %s", exec.Phase, exec.Spec.Executable),
		DryRun:   true,
	}, nil
}
