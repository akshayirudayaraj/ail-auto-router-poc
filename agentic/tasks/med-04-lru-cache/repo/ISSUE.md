# Issue

`LRUCache(capacity)` should evict the LEAST-recently-used key on overflow, and a `get` must count as a use (refreshing recency). Currently `get` does not refresh recency, so the wrong key is evicted. See tests for the exact expected eviction behavior.
