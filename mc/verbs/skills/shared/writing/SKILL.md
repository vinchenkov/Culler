---
name: writing
description: The writing standard for every text that a human or another agent reads — a spec, a plan, a task brief, a verdict, a review packet, a commit message, a reply to the operator. Load it before you write one.
---

# Writing

Apply this skill to each text that a human or another agent reads. This
includes:

- a specification, a requirement record, a plan, or a design record;
- a task brief or an instruction file for another agent;
- a verdict, a review packet, a report, or an evidence note;
- a commit message or a pull request description;
- a reply to the operator;
- a comment in code.

## Domain language

**Never invent language.**

Do not make up a term. Use only the terms that the project or the domain
already uses. When no established term exists, describe the thing in plain
words. Do not give it a name.

Use established project or domain terms. Use the same term for the same concept throughout the conversation and all artifacts. Do not alternate between synonyms for the same concept.  Define each abbreviation at first use.

## Write in the declarative style

This section applies to a specification, a plan, a task brief, and a design
record.

- State the required end state. Do not state the steps that make the change.
- Let the implementing agent select the low-level details.
- Name an implementation element only when its identity is part of the
  requirement. Example: name a class when the requirement controls the behavior
  of that class. Do not name the functions of that class.
- Write the outcomes, the constraints, and the invariants.

Use this test on each requirement. Imagine that an agent reads the text two
days after you write it. The requirement must stay correct after small changes
to strings, names, and references in the repository.

This is an anti-example. Do not write text of this type:

```
- Dev. §40's 0777 log-leaf justification re-keys from `a sealed Verifier run
  adds `--user 10002:10002` to the role/route-keyed uid-10002 hardening
  (D5). The mode, the uid, and the reason are unchanged.
```

That text gives a change history and external references. Write the required
mode, the required user id, and the reason as a direct requirement instead.

## Write the text as the final source of truth

- Make each instruction self-contained. Write for a reader who has no
  conversation context.
- Include only the information that defines the required system.
- State the required behavior, constraint, or invariant directly.
- Remove an instruction by omission. Delete each reference to it. Do not write a
  counter-instruction.
    - Example: to remove a "24-hour rebuild budget", delete each mention of the
      budget. Do not write "There is no 24-hour rebuild budget."

Do not include these items in a specification, a plan, or a design record:

- the decision history;
- previous requirements;
- rejected alternatives;
- the implementation history;
- the behavior history.

If a past event is important, show the result in the outcomes, the constraints,
and the invariants. Do not write commentary.

## Write for two audiences

Agents read the text. Humans must also understand the text. Write in ASD-STE100
Simplified Technical English (STE).

## Writing standard — ASD-STE100 Simplified Technical English

Write all text in STE. This is a rule, not a preference.

1. **Use one word for one thing.** Select a word, then use the same word each
   time you write about that thing. Do not use a synonym for variety.
2. **Keep sentences short.** An instruction can have a maximum of 20 words. A
   description can have a maximum of 25 words.
3. **Keep paragraphs short.** A paragraph can have a maximum of 6 sentences.
4. **Put one idea in one sentence.**
5. **Use the active voice.** Write "Thinking Machines opened 8 jobs". Do not
   write "8 jobs were opened".
6. **Use the simple tenses only** — simple past, simple present, simple future.
   Do not write "has grown" or "had been listed". Write "grew" and "was listed".
7. **Do not use an -ing word as a noun or as an adjective.** Write "the board
   changed", not "the changing board" or "the changing of the board".
8. **Use articles.** Write "the job board". Do not write "job board".
9. **Do not delete words to make the text short.** Write complete sentences.
10. **Use a maximum of 3 nouns together.** "research infrastructure role" is
    correct. "board expansion follow-up queue item" is not correct.
11. **Write positive statements.** Write "The page shows 24 posts". Do not write
    "The page does not show fewer than 24 posts". To state the opposite, write
    "The page shows less than 24 posts."
12. **Use "can" for ability. Use "must" for a requirement. Use "do not" for a
    prohibition.** Do not write "should", "ought to", or "may want to".
13. **Do not use jargon, idiom, metaphor, humor, or shorthand.**
14. **Write an abbreviation in full the first time you use it in a record.**
    Write "Forward Deployed Engineer (FDE)", then write "FDE" after that.
15. **Use a vertical list when you have more than two related items.**

## Check the text before you deliver it

Read the text again and confirm each item:

- Each term is a term that the project or the domain already uses.
- Each requirement states an end state.
- The text names an implementation element only when the identity of that
  element is part of the requirement.
- The text holds no decision history, no rejected alternative, and no
  implementation history.
- A reader with no conversation context can act on the text.
- Each sentence follows the 15 STE rules.
