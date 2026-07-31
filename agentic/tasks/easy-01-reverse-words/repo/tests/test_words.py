from strkit.words import reverse_words

def test_reverses_order():
    assert reverse_words('the quick brown fox') == 'fox brown quick the'

def test_single_word():
    assert reverse_words('hello') == 'hello'

def test_empty():
    assert reverse_words('') == ''
