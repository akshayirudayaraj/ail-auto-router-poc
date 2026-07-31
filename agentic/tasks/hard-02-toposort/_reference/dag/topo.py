def topo_sort(graph):
    # graph: {node: [deps]}, edge dep -> node
    indeg = {}
    adj = {}
    nodes = set(graph)
    for node, deps in graph.items():
        nodes.update(deps)
    for n in nodes:
        indeg.setdefault(n, 0)
    for node, deps in graph.items():
        for d in deps:
            adj.setdefault(d, []).append(node)
            indeg[node] = indeg.get(node, 0) + 1
    queue = sorted(n for n in nodes if indeg[n] == 0)
    order = []
    while queue:
        n = queue.pop(0)
        order.append(n)
        for m in adj.get(n, []):
            indeg[m] -= 1
            if indeg[m] == 0:
                queue.append(m)
        queue.sort()
    if len(order) != len(nodes):
        raise ValueError('cycle')
    return order
