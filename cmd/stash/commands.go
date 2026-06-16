package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/lightning-rider-999/go-stashapp/stash"
)

// commandSpec is one row of the generated operation table. The generator
// (genops EmitCommands) writes a []commandSpec literal into gen_commands.go;
// buildRootCommand turns each spec into a leaf cobra command. Query is the
// genqlient operation const (stash.<OpName>_Operation), so the GraphQL text
// lives in exactly one place and a server upgrade that drifts it is a red build.
type commandSpec struct {
	// Path is the cobra command path, resource-then-verb (["scene", "list"]).
	Path []string
	// OpName is the exported operation name, also the query const stem.
	OpName string
	// Query is the genqlient operation document for this field.
	Query string
	// Kind is "query", "mutation", or "subscription".
	Kind string
	// InputType is the base type of the "input" argument, or "" if none.
	InputType string
	// ReturnType is the base named type the operation returns.
	ReturnType string
	// Destructive flags an operation the overlay marked as data-destroying.
	Destructive bool
	// JobReturning flags an operation that enqueues an async job.
	JobReturning bool
	// Deprecated flags a field carrying @deprecated in the schema.
	Deprecated bool
}

// buildRootCommand assembles the full cobra tree from generatedCommands. It
// creates a group command for every Path prefix (scene, metadata, ...) and a
// leaf command per spec under it. The leaf's RunE reads variables from --input
// (file or stdin), runs the operation as raw GraphQL through the shared SDK
// transport, and writes the response data to stdout.
//
// Persistent flags on the root configure the client and output:
//
//	--url        Stash base URL (falls back to STASHAPP_URL in the client)
//	--api-key    Stash API key (falls back to STASHAPP_API_KEY in the client)
//	--output/-o  output format: json (default), ndjson, table, yaml
//	--input      variables source: a JSON file path, or "-" for stdin
func buildRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "stash",
		Short: "Agent-first CLI for the Stash GraphQL API",
		Long: "stash is a machine-readable command-line client for a self-hosted Stash " +
			"instance. Every Stash GraphQL operation is exposed as a resource-and-verb " +
			"command (e.g. `stash scene list`, `stash metadata scan`). Output is JSON by " +
			"default.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// ArbitraryArgs bypasses cobra's root-only "unknown command" arg check so
		// an unrecognised top-level command reaches helpOrUnknown and is reported
		// as a usage error (exit 2), consistent with the resource groups.
		Args: cobra.ArbitraryArgs,
		RunE: helpOrUnknown,
		// Version is the injected binary version (main.version), which makes cobra
		// register the built-in --version flag. The template widens the output to
		// the commit and build date so a bug report pins an exact build. This is
		// the CLI binary's own version, distinct from the `stash misc version`
		// GraphQL op, which reports the connected Stash server's version.
		Version: version,
	}
	root.SetVersionTemplate(
		"stash {{.Version}} (commit " + commit + ", built " + date + ")\n",
	)
	wrapCobraUsageErrors(root)

	root.PersistentFlags().String("url", "", "Stash base URL (default $STASHAPP_URL)")
	root.PersistentFlags().String("api-key", "", "Stash API key (default $STASHAPP_API_KEY)")
	root.PersistentFlags().StringP("output", "o", "json", "output format: "+strings.Join(outputFormats, ", "))
	root.PersistentFlags().String("input", "", "variables source: JSON file path, or \"-\" for stdin")

	// groups caches intermediate group commands by their joined prefix so a
	// resource group (scene) is created once and shared by all its leaves.
	groups := map[string]*cobra.Command{}

	specs := append([]commandSpec(nil), generatedCommands...)
	sort.Slice(specs, func(i, j int) bool {
		return strings.Join(specs[i].Path, " ") < strings.Join(specs[j].Path, " ")
	})

	for _, spec := range specs {
		parent := ensureGroups(root, groups, spec.Path[:len(spec.Path)-1])
		leaf := newLeafCommand(spec)
		parent.AddCommand(leaf)
	}

	// catalog is a built-in (not a generated GraphQL operation): it serves the
	// embedded operation catalog without touching the server.
	root.AddCommand(newCatalogCommand())
	return root
}

// helpOrUnknown is the RunE for the root and resource-group commands. With no
// arguments it prints help and exits 0; an unrecognised subcommand becomes a
// usage error (exit 2) rather than cobra's default of a silent help dump on a
// zero exit, so an agent that mistypes a command sees a non-zero status.
func helpOrUnknown(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return newUsageError(fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath()))
	}
	return c.Help()
}

// ensureGroups walks the prefix segments, creating and caching a group command
// for each, and returns the command the leaf should attach to. An empty prefix
// returns the root.
func ensureGroups(root *cobra.Command, groups map[string]*cobra.Command, prefix []string) *cobra.Command {
	parent := root
	for i := range prefix {
		key := strings.Join(prefix[:i+1], " ")
		g, ok := groups[key]
		if !ok {
			g = &cobra.Command{
				Use:           prefix[i],
				Short:         fmt.Sprintf("%s operations", prefix[i]),
				SilenceUsage:  true,
				SilenceErrors: true,
				RunE:          helpOrUnknown,
			}
			groups[key] = g
			parent.AddCommand(g)
		}
		parent = g
	}
	return parent
}

// clientResolver builds the *stash.Client a leaf command runs against. The seam
// exists so a test can drive the full RunE — gating, --wait, streaming — against
// an in-memory client without setting environment variables. Production passes
// clientFromFlags; a test passes a closure returning its own client.
type clientResolver func(cmd *cobra.Command) (*stash.Client, error)

// newLeafCommand builds the leaf cobra command for one operation spec, resolving
// its client from the --url/--api-key flags.
func newLeafCommand(spec commandSpec) *cobra.Command {
	return newLeafCommandResolver(spec, clientFromFlags)
}

// newLeafCommandWithClient builds a leaf bound to a fixed client, for tests.
func newLeafCommandWithClient(spec commandSpec, c *stash.Client) *cobra.Command {
	return newLeafCommandResolver(spec, func(*cobra.Command) (*stash.Client, error) {
		return c, nil
	})
}

// newLeafCommandResolver builds the leaf cobra command for one operation spec
// using resolve to obtain the client. The RunE wiring, in order:
//
//  1. Destructive gate. A destructive op without --yes-i-understand refuses
//     before any client is built, so a refused op never reaches the server.
//  2. Subscription. The three subscription leaves stream NDJSON events until ctx
//     is cancelled; see streamForSpec.
//  3. Variables + client. Read --input/flags, build the client.
//  4. Job-returning + --wait. A job-returning mutation invoked with --wait runs,
//     extracts the job ID, and tracks the job to a terminal state; see runWait.
//     Without --wait it falls through to the plain execute path.
//  5. Plain execute. runOperation runs the op and renders the response.
func newLeafCommandResolver(spec commandSpec, resolve clientResolver) *cobra.Command {
	leaf := &cobra.Command{
		Use:   spec.Path[len(spec.Path)-1],
		Short: shortFor(spec),
	}
	if spec.Deprecated {
		leaf.Deprecated = "deprecated in the Stash schema; prefer the current operation"
	}
	addConvenienceFlags(leaf, spec)
	addDestructiveFlag(leaf, spec)
	addWaitFlags(leaf, spec)

	leaf.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := checkDestructiveGate(cmd, spec); err != nil {
			return err
		}

		if spec.Kind == "subscription" {
			client, err := resolve(cmd)
			if err != nil {
				return err
			}
			return streamForSpec(cmd.Context(), client, spec, cmd.OutOrStdout())
		}

		vars, err := resolveVariables(cmd, spec)
		if err != nil {
			return err
		}

		client, err := resolve(cmd)
		if err != nil {
			return err
		}

		if spec.JobReturning && waitRequested(cmd) {
			format, _ := cmd.Flags().GetString("output")
			return runWait(cmd, client, spec, vars, format)
		}

		format, _ := cmd.Flags().GetString("output")
		return runOperation(cmd.Context(), client, spec, vars, format, cmd.OutOrStdout())
	}
	return leaf
}

// shortFor renders a one-line description for a leaf, tagging destructive and
// job-returning operations so the hazard is visible in help output.
func shortFor(spec commandSpec) string {
	desc := fmt.Sprintf("%s (%s)", spec.OpName, spec.Kind)
	switch {
	case spec.Destructive && spec.JobReturning:
		return desc + " [destructive, async job]"
	case spec.Destructive:
		return desc + " [destructive]"
	case spec.JobReturning:
		return desc + " [async job]"
	}
	return desc
}

// clientFromFlags builds a *stash.Client from the root --url/--api-key flags,
// each falling back to its environment variable inside NewClient when the flag
// is empty.
func clientFromFlags(cmd *cobra.Command) (*stash.Client, error) {
	url, _ := cmd.Flags().GetString("url")
	apiKey, _ := cmd.Flags().GetString("api-key")

	opts := []stash.Option{}
	if url != "" {
		opts = append(opts, stash.WithURL(url))
	}
	if apiKey != "" {
		opts = append(opts, stash.WithAPIKey(apiKey))
	}
	return stash.NewClient(opts...)
}

// graphqlVars adapts a map of raw-JSON variables to the genqlient request
// variable shape. genqlient marshals Request.Variables, so a
// map[string]json.RawMessage round-trips each value verbatim — which is what a
// later task's three-state input binding needs.
func graphqlVars(vars map[string]json.RawMessage) any {
	if len(vars) == 0 {
		return map[string]json.RawMessage{}
	}
	return vars
}

// requestFor builds the genqlient request for a spec and its variables.
func requestFor(spec commandSpec, vars map[string]json.RawMessage) *graphql.Request {
	return &graphql.Request{
		OpName:    spec.OpName,
		Query:     spec.Query,
		Variables: graphqlVars(vars),
	}
}
