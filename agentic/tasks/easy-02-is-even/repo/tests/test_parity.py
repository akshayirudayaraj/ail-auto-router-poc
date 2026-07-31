from numkit.parity import is_even

def test_even_true():
    assert is_even(4) is True
    assert is_even(0) is True

def test_odd_false():
    assert is_even(3) is False
    assert is_even(7) is False
