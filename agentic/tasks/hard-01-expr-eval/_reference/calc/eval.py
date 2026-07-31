def evaluate(expr):
    tokens = _tokenize(expr)
    val, pos = _parse_expr(tokens, 0)
    return val


def _tokenize(expr):
    tokens = []
    i = 0
    expr = expr.replace(' ', '')
    while i < len(expr):
        ch = expr[i]
        if ch.isdigit():
            j = i
            while j < len(expr) and expr[j].isdigit():
                j += 1
            tokens.append(int(expr[i:j]))
            i = j
        else:
            tokens.append(ch)
            i += 1
    return tokens


def _parse_expr(tokens, pos):
    val, pos = _parse_term(tokens, pos)
    while pos < len(tokens) and tokens[pos] in ('+', '-'):
        op = tokens[pos]
        rhs, pos = _parse_term(tokens, pos + 1)
        val = val + rhs if op == '+' else val - rhs
    return val, pos


def _parse_term(tokens, pos):
    val, pos = _parse_factor(tokens, pos)
    while pos < len(tokens) and tokens[pos] in ('*', '/'):
        op = tokens[pos]
        rhs, pos = _parse_factor(tokens, pos + 1)
        val = val * rhs if op == '*' else val // rhs
    return val, pos


def _parse_factor(tokens, pos):
    if tokens[pos] == '(':
        val, pos = _parse_expr(tokens, pos + 1)
        return val, pos + 1  # skip ')'
    return tokens[pos], pos + 1
