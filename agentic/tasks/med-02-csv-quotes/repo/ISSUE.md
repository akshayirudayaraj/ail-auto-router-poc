# Issue

`parse_line` splits a single CSV row into fields on commas, but it must respect double-quoted fields: a comma inside quotes is part of the field, not a separator, and the surrounding quotes are stripped. parse_line('a,"b,c",d') must return ['a', 'b,c', 'd']. Plain rows must keep working.
