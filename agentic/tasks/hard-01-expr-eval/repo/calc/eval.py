def evaluate(expr):
    expr = expr.replace(' ', '')
    # NOTE: strictly left-to-right, no precedence, no parens.
    num = ''
    result = None
    op = '+'
    for ch in expr:
        if ch.isdigit():
            num += ch
            continue
        result = _apply(result, op, int(num))
        num = ''
        op = ch
    return _apply(result, op, int(num))


def _apply(result, op, val):
    if result is None:
        return val
    if op == '+':
        return result + val
    if op == '-':
        return result - val
    if op == '*':
        return result * val
    if op == '/':
        return result // val
    raise ValueError(op)
