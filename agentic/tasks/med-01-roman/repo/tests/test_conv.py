from roman.conv import to_roman

def test_subtractive():
    assert to_roman(4) == 'IV'
    assert to_roman(9) == 'IX'
    assert to_roman(40) == 'XL'
    assert to_roman(90) == 'XC'
    assert to_roman(400) == 'CD'
    assert to_roman(900) == 'CM'

def test_composite():
    assert to_roman(1994) == 'MCMXCIV'
    assert to_roman(2023) == 'MMXXIII'

def test_simple_still_ok():
    assert to_roman(3) == 'III'
    assert to_roman(10) == 'X'
