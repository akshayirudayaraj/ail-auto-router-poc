from calc.eval import evaluate

def test_precedence():
    assert evaluate('2+3*4') == 14
    assert evaluate('2*3+4') == 10

def test_parens():
    assert evaluate('2*(3+4)') == 14
    assert evaluate('(1+2)*(3+4)') == 21

def test_mixed():
    assert evaluate('10-2*3') == 4
    assert evaluate('100/5/2') == 10

def test_single():
    assert evaluate('42') == 42
