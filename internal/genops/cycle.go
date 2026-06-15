package genops

import (
	"sort"
	"strings"
)

// Path-based cycle termination
//
// genqlient fragments must form a directed acyclic graph: a fragment may not
// (transitively) spread itself. genops keeps the graph acyclic without a
// hand-maintained stop-list (B5):
//
//   - Nested entity edges resolve to a <T>Ref leaf, never the full <T>Fields
//     (B2), so every cycle through a ref-able type is broken at depth one.
//   - Value-type edges (file/folder metadata) are expanded until the walk
//     revisits a type already on the DFS path, at which point the edge is
//     terminated with a scalars-only inline selection (recorded as path-named).
//
// spreadGraph and FragmentCycles let the conformance suite assert the invariant
// directly against the emitted text.

// spreadGraph maps each fragment to the fragment names it spreads (via a
// `...Name` selection), parsed from the generated fragment bodies. Inline
// fragments (`... on Type`) are not spreads and are ignored.
func spreadGraph(fs *FragmentSet) map[string][]string {
	g := make(map[string][]string, len(fs.bodies))
	for _, name := range fs.Names() {
		body, _ := fs.Fragment(name)
		seen := map[string]bool{}
		var spreads []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "...") {
				continue
			}
			rest := strings.TrimSpace(strings.TrimPrefix(line, "..."))
			if rest == "" || strings.HasPrefix(rest, "on ") {
				continue // inline fragment, not a named spread
			}
			frag := strings.Fields(rest)[0]
			if !seen[frag] {
				seen[frag] = true
				spreads = append(spreads, frag)
			}
		}
		sort.Strings(spreads)
		g[name] = spreads
	}
	return g
}

// FragmentCycles returns each cycle in the fragment spread graph as the list of
// fragment names on the cycle. An empty result means the path-based termination
// produced a valid acyclic fragment DAG.
func FragmentCycles(fs *FragmentSet) [][]string {
	g := spreadGraph(fs)
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := map[string]int{}
	var stack []string
	var cycles [][]string

	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)

	var visit func(string)
	visit = func(n string) {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range g[n] {
			switch color[m] {
			case white:
				visit(m)
			case gray:
				// Back-edge: extract the cycle from the stack.
				for i, s := range stack {
					if s == m {
						cycle := append([]string(nil), stack[i:]...)
						cycles = append(cycles, append(cycle, m))
						break
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}

	for _, n := range names {
		if color[n] == white {
			visit(n)
		}
	}
	return cycles
}
