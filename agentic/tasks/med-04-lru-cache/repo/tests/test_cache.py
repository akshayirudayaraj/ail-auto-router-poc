from lru.cache import LRUCache

def test_get_refreshes_recency():
    c = LRUCache(2)
    c.put('a', 1)
    c.put('b', 2)
    assert c.get('a') == 1   # 'a' now most-recently used
    c.put('c', 3)            # should evict 'b', not 'a'
    assert c.get('b') is None
    assert c.get('a') == 1
    assert c.get('c') == 3

def test_basic_evict():
    c = LRUCache(1)
    c.put('a', 1)
    c.put('b', 2)
    assert c.get('a') is None
    assert c.get('b') == 2
