package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
)

const (
	defaultTimeout = 10 * time.Second
	// Pairing may wait for a person to confirm a passkey, so it gets longer
	// unless --timeout is given or nobody can answer.
	defaultPairTimeout   = 60 * time.Second
	defaultWatchInterval = 2 * time.Second
)

// options holds every flag. Flags may appear anywhere on the command line;
// "--" ends flag parsing.
type options struct {
	mode           output.Mode
	verbose        bool
	nonInteractive bool
	timeout        time.Duration
	timeoutSet     bool
	help           bool
	version        bool

	// Command flags, validated against the command in validateFlags.
	withResponse    bool
	withoutResponse bool
	interval        time.Duration
	intervalSet     bool
	all             bool
}

func parseArgs(args []string) (options, []string, error) {
	o := options{timeout: defaultTimeout, interval: defaultWatchInterval}
	var jsonOut, quiet bool
	var positional []string
	var parseErr error
	fail := func(format string, a ...any) {
		if parseErr == nil {
			parseErr = domain.NewError(domain.CodeUsage, format, a...)
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !isFlag(arg) {
			positional = append(positional, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		takeValue := func() string {
			if hasValue {
				return value
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			fail("%s requires a value", name)
			return ""
		}
		switch name {
		case "--json":
			jsonOut = true
		case "-q", "--quiet":
			quiet = true
		case "-v", "--verbose":
			o.verbose = true
		case "--non-interactive":
			o.nonInteractive = true
		case "-h", "--help":
			o.help = true
		case "--version":
			o.version = true
		case "--with-response":
			o.withResponse = true
		case "--without-response":
			o.withoutResponse = true
		case "--all":
			o.all = true
		case "--timeout":
			o.timeout, o.timeoutSet = parsePositiveDuration(name, takeValue(), fail), true
		case "--interval":
			o.interval, o.intervalSet = parsePositiveDuration(name, takeValue(), fail), true
		default:
			fail("unknown flag %s", name)
		}
		if hasValue && !flagTakesValue(name) {
			fail("%s does not take a value", name)
		}
	}
	// Choose the output mode before reporting any error, so that even a
	// usage error is rendered as JSON when --json was given.
	switch {
	case jsonOut:
		o.mode = output.JSON
	case quiet:
		o.mode = output.Quiet
	}
	if jsonOut && quiet {
		fail("--json and --quiet cannot be combined")
	}
	if o.withResponse && o.withoutResponse {
		fail("--with-response and --without-response cannot be combined")
	}
	return o, positional, parseErr
}

// isFlag reports whether arg is a flag rather than a value. A leading "-"
// followed by a digit is a negative number, as in "bt set dev level -3".
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-' && (arg[1] < '0' || arg[1] > '9')
}

func flagTakesValue(name string) bool { return name == "--timeout" || name == "--interval" }

func parsePositiveDuration(name, value string, fail func(string, ...any)) time.Duration {
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		fail("%s must be a positive duration such as 10s, not %q", name, value)
		return 0
	}
	return d
}

// validateFlags rejects command flags given to a command that ignores them.
func validateFlags(o options, c *command, args []string) error {
	gattWrite := c.name == "gatt" && len(args) > 1 && args[1] == "write"
	if (o.withResponse || o.withoutResponse) && !gattWrite {
		return domain.NewError(domain.CodeUsage, "--with-response and --without-response apply only to gatt write")
	}
	if o.intervalSet && c.name != "watch" {
		return domain.NewError(domain.CodeUsage, "--interval applies only to watch")
	}
	if o.all && c.name != "get" {
		return domain.NewError(domain.CodeUsage, "--all applies only to get")
	}
	if c.name == "get" && len(args) != map[bool]int{true: 1, false: 2}[o.all] {
		return domain.NewError(domain.CodeUsage, "usage: bt %s", c.usage)
	}
	return nil
}

// command is one entry of the CLI's command table, which also drives help.
type command struct {
	name    string
	usage   string
	summary string
	details string // optional, shown by bt help <command>
	minArgs int
	maxArgs int // -1 for unlimited
	run     func(a *application, ctx context.Context, args []string) error
	// streams reports whether the invocation runs until cancelled, so the
	// global deadline must not apply.
	streams func(args []string) bool
}

func (c *command) checkArgs(args []string) error {
	if len(args) < c.minArgs || (c.maxArgs >= 0 && len(args) > c.maxArgs) {
		return domain.NewError(domain.CodeUsage, "usage: bt %s", c.usage)
	}
	return nil
}

func findCommand(name string) *command {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i]
		}
	}
	return nil
}

func unknownCommand(name string) error {
	if suggestion := suggestCommand(name); suggestion != "" {
		return domain.NewError(domain.CodeUsage, "unknown command %q; did you mean %q? Run bt help for all commands", name, suggestion)
	}
	return domain.NewError(domain.CodeUsage, "unknown command %q; run bt help for all commands", name)
}

// suggestCommand returns the command closest to name, if it is a likely typo.
func suggestCommand(name string) string {
	best, bestDistance := "", 3
	for _, c := range commands {
		if d := editDistance(name, c.name); d < bestDistance {
			best, bestDistance = c.name, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func helpText(c *command) string {
	if c != nil {
		text := fmt.Sprintf("usage: bt %s\n\n%s", c.usage, c.summary)
		if c.details != "" {
			text += "\n\n" + c.details
		}
		return text + "\n\n" + globalFlagsHelp
	}
	var b strings.Builder
	b.WriteString("Betty (bt): one command for every Bluetooth device, on every OS\n\nUsage:\n  bt <command> [arguments] [flags]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-46s %s\n", c.usage, c.summary)
	}
	b.WriteString("\n" + globalFlagsHelp + "\n\nA <device> is a device ID, a Bluetooth address, or a unique part of a device name.")
	return b.String()
}

const globalFlagsHelp = `Flags (allowed anywhere):
  --json              print schema-versioned JSON; errors and warnings go to stderr
  -q, --quiet         print nothing on success
  -v, --verbose       print diagnostics to stderr
  --non-interactive   fail instead of waiting for a person
  --timeout <dur>     bound the command (default 10s; pairing 60s)
  -h, --help          show help
  --version           show the version`
