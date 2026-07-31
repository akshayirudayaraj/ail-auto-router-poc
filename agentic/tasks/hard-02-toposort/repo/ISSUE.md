# Issue

`topo_sort(graph)` returns a topological ordering of a DAG given as {node: [deps...]} where an edge dep->node means dep must come first. Two bugs: (1) it does not detect cycles — on a cyclic graph it should raise ValueError('cycle'); (2) nodes that appear only as dependencies (never as a key) are dropped from the output. Every node must appear exactly once, and the order must respect dependencies.
