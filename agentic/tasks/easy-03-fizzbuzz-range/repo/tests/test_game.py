from fb.game import fizzbuzz

def test_five():
    assert fizzbuzz(5) == ['1', '2', 'Fizz', '4', 'Buzz']

def test_fifteen_last():
    assert fizzbuzz(15)[-1] == 'FizzBuzz'

def test_len():
    assert len(fizzbuzz(10)) == 10
