from intervals.merge import merge

def test_touching_merge():
    assert merge([[1, 4], [4, 5]]) == [[1, 5]]

def test_overlap():
    assert merge([[1, 3], [2, 6], [8, 10], [15, 18]]) == [[1, 6], [8, 10], [15, 18]]

def test_disjoint_stays():
    assert merge([[1, 2], [5, 6]]) == [[1, 2], [5, 6]]
