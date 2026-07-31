# Issue

`lcs(a, b)` should return the LONGEST COMMON SUBSEQUENCE of two sequences as a list. It builds the DP length table correctly but the backtracking is wrong: it returns a reversed and/or truncated result. lcs('ABCBDAB', 'BDCAB') should be a length-4 common subsequence such as list('BCAB') or list('BDAB'). Fix the backtracking so the returned sequence is a real common subsequence of maximal length, in order.
