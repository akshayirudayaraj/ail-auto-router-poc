from wordkit.counter import word_count


def test_word_count_basic():
    assert word_count("a b c") == 3


def test_word_count_extra_spaces():
    assert word_count("  a   b  ") == 2
