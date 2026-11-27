Review the added lines.

Reply with a single JSON object and nothing else:
{"findings":[{"line":<int>,"end_line":<int>,"severity":"critical|high|medium|low","title":"<short, under 80 chars>","body":"<1-3 sentences: what is wrong, when it breaks, and the impact>","example":"<concrete failing case: input -> what happens, what should happen>","suggestion":"<replacement code or empty string>","confidence":<0.0-1.0>}]}

About "suggestion": if a small, certain fix exists, give the exact replacement text for lines line..end_line (inclusive) of the new file: full lines, original indentation, no diff markers, no line numbers, no code fences. It will be applied verbatim as a GitHub suggested change, replacing exactly those lines, so it must keep everything on them that's still needed. Otherwise use "".
