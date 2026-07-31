def topo_sort(graph):
    # graph: {node: [deps]}, edge dep -> node
    indeg = {}
    adj = {}
    for node, deps in graph.items():
        indeg.setdefault(node, 0)
        for d in deps:
            adj.setdefault(d, []).append(node)
            indeg[node] = indeg.get(node, 0) + 1
    queue = [n for n in graph if indeg.get(n, 0) == 0]
    order = []
    while queue:
        n = queue.pop(0)
        order.append(n)
        for m in adj.get(n, []):
            indeg[m] -= 1
            if indeg[m] == 0:
                queue.append(m)
    return order
