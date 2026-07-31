import pytest
from dag.topo import topo_sort


def _valid_order(graph, order):
    nodes = set(graph)
    for deps in graph.values():
        nodes.update(deps)
    if set(order) != nodes or len(order) != len(nodes):
        return False
    pos = {n: i for i, n in enumerate(order)}
    for node, deps in graph.items():
        for d in deps:
            if pos[d] > pos[node]:
                return False
    return True


def test_includes_dep_only_nodes():
    g = {'b': ['a'], 'c': ['b']}   # 'a' only appears as a dep
    order = topo_sort(g)
    assert _valid_order(g, order)

def test_cycle_detected():
    g = {'a': ['b'], 'b': ['a']}
    with pytest.raises(ValueError):
        topo_sort(g)

def test_diamond():
    g = {'d': ['b', 'c'], 'b': ['a'], 'c': ['a'], 'a': []}
    order = topo_sort(g)
    assert _valid_order(g, order)
