from csvmini.parse import parse_line

def test_quoted_comma():
    assert parse_line('a,"b,c",d') == ['a', 'b,c', 'd']

def test_quotes_stripped():
    assert parse_line('"x","y"') == ['x', 'y']

def test_plain():
    assert parse_line('a,b,c') == ['a', 'b', 'c']
