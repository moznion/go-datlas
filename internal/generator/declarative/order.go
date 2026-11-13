package declarative

import "sort"

// datasetOrder returns the dataset names in dependency order: every dataset comes after the
// datasets of the same package it refers to, so that applying them in this order inserts
// referenced records first. Datasets without an order between them are in name order.
// Datasets referring to each other in a cycle cannot be ordered; they are adjacent, in name order.
// References to the dataset itself and to master data of other packages are ignored.
func datasetOrder(datasets []*Dataset) []string {
	deps := map[string]map[string]bool{}
	for _, ds := range datasets {
		if deps[ds.Name] == nil {
			deps[ds.Name] = map[string]bool{}
		}
		for _, r := range ds.Schema.Refs {
			if r.Package == "" && r.Dataset != ds.Name {
				deps[ds.Name][r.Dataset] = true
			}
		}
	}
	var names []string
	for n := range deps {
		names = append(names, n)
	}
	sort.Strings(names)
	sorted := func(set map[string]bool) []string {
		var xs []string
		for x := range set {
			if _, ok := deps[x]; ok {
				xs = append(xs, x)
			}
		}
		sort.Strings(xs)
		return xs
	}

	// Tarjan's algorithm groups the datasets into strongly connected components (cycles).
	var (
		index   = map[string]int{}
		low     = map[string]int{}
		onStack = map[string]bool{}
		stack   []string
		comp    = map[string]int{}
		members [][]string
		visit   func(string)
	)
	visit = func(n string) {
		index[n], low[n] = len(index), len(index)
		stack = append(stack, n)
		onStack[n] = true
		for _, d := range sorted(deps[n]) {
			if _, seen := index[d]; !seen {
				visit(d)
				low[n] = min(low[n], low[d])
			} else if onStack[d] {
				low[n] = min(low[n], index[d])
			}
		}
		if low[n] == index[n] {
			var ms []string
			for {
				m := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[m] = false
				comp[m] = len(members)
				ms = append(ms, m)
				if m == n {
					break
				}
			}
			sort.Strings(ms)
			members = append(members, ms)
		}
	}
	for _, n := range names {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}

	// Order the components topologically, taking the one with the smallest name first.
	pending := make([]map[int]bool, len(members))
	for c, ms := range members {
		pending[c] = map[int]bool{}
		for _, m := range ms {
			for d := range deps[m] {
				if dc, ok := comp[d]; ok && dc != c {
					pending[c][dc] = true
				}
			}
		}
	}
	done := make([]bool, len(members))
	var order []string
	for range members {
		next := -1
		for c, ms := range members {
			if done[c] || len(pending[c]) > 0 {
				continue
			}
			if next < 0 || ms[0] < members[next][0] {
				next = c
			}
		}
		done[next] = true
		order = append(order, members[next]...)
		for c := range pending {
			delete(pending[c], next)
		}
	}
	return order
}
