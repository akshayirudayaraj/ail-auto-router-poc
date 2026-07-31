# Issue

`evaluate(expr)` is a calculator for +,-,*,/ over integers with parentheses. It ignores operator precedence (it evaluates strictly left-to-right) so evaluate('2+3*4') returns 20 instead of 14, and it does not handle parentheses: evaluate('2*(3+4)') should be 14. Rewrite the evaluator to respect precedence and parentheses. Division is integer floor division; assume well-formed input.
