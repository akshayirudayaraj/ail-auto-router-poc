# Issue

`merge(intervals)` merges overlapping [start,end] intervals. It treats intervals that merely TOUCH (e.g. [1,2] and [2,3]) as separate; they should merge into [1,3]. Also the output must be sorted by start. merge([[1,3],[2,6],[8,10],[15,18]]) == [[1,6],[8,10],[15,18]] and merge([[1,4],[4,5]]) == [[1,5]].
