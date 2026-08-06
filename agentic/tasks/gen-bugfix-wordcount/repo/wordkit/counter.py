def word_count(text):
    # BUG: splits on a single space, so runs of spaces yield empty "words".
    return len(text.split(" "))
