from lcs.core import lcs


def _is_subseq(sub, seq):
    it = iter(seq)
    return all(x in it for x in sub)


def test_length_and_validity():
    a, b = 'ABCBDAB', 'BDCAB'
    r = lcs(a, b)
    assert len(r) == 4
    assert _is_subseq(r, a)
    assert _is_subseq(r, b)

def test_identical():
    assert lcs('abc', 'abc') == list('abc')

def test_disjoint():
    assert lcs('abc', 'xyz') == []
