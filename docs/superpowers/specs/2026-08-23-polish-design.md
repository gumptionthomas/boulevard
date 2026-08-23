# Milestone 5 — polish

**Status:** design agreed 23 August 2026.

A sweep, not a redesign. Nothing here changes what the software does: no new
routes, no schema change, no new behaviour. What changes is what the software
*says*, how big it says it, and where it puts the controls.

Section numbers cite `DESIGN.md` as it stands on 23 August 2026.

## 1. What this milestone corrects

### It amends §6's claim about the empty state

§6 says the empty state "is arguably the most important screen in the
application." That is wrong, and this milestone amends it.

Empty is rare. It is mostly the steward's own first hour, plus the occasional
visitor. The real gap is **orientation**, and orientation is missing on
**every** shelf, not just an empty one. A stocked shelf shows notes and links
but never says what the thing is or how it changes; someone arriving from a
shared link at a full shelf is exactly as lost as at an empty one.

The empty state stops being a special screen. It becomes the ordinary shelf
page with nothing in the middle — which is what it always was.

### It removes the assumption that a Boulevard is a Little Free Library

An LFL is one demographic. A Boulevard may be a sandwich board, a garage
door, or a fence. Copy that says "the box", "inside the box door", or names
Little Free Library directly excludes every steward who has none of those.

The vocabulary changes accordingly (§2).

### It does not claim a scan distance

The two printed artifacts differ in purpose but barely in size:

| | shelf sign | take/leave card |
|---|---|---|
| overall | 4.2 × 2.2 in | 3.5 × 2.0 in |
| QR | **1.5 in** | **1.15 in** |

A QR scans reliably at roughly ten times its width — about 15 inches for the
sign and 11 for the card. Both are up-close codes. Copy that says "scan the
big one from a distance" would be describing an artifact that does not exist.

**Milestone 5.5 makes it true**: the shelf code becomes its own full page at
6–7 inches, readable from several feet. Until then this milestone's copy
distinguishes the two artifacts by **what they do**, never by how far away
they work.

## 2. Vocabulary

**"The shelf" is one word for the digital list and the physical spot.** A
reader meets "the shelf is empty" and "scan the card at the shelf" on the
same page, and that is intended: they are the same thing. No new noun to
learn, and it is true of an LFL, a sandwich board, and a garage.

**The two artifacts get names:**

- **the shelf code** — mounted, permanent, browses the shelf, mints nothing
- **the take/leave card** — placed close, replaced monthly, grants a session

**Removed from all user-facing copy:** "the box", "the box door", "inside the
box door", "Little Free Library". This covers 12 template instances, 21 Go
strings, and the booklet cover's install steps.

**Not removed from internal prose.** `DESIGN.md`'s own argument, code
comments, and commit messages may go on saying "the box" where it reads
naturally — this is a change to what users are shown, not a find-and-replace
across the repository. The distinction matters: a comment explaining why a
card is small enough to resist being scanned from a passing car is clearer
for naming the physical object plainly.

## 3. Copy

**Terse.** Every user-facing string gets read once, and the test is: write
it, delete the second sentence, check whether it still works. The shelf is
read one-handed, outdoors, in bad light, by someone who did not come looking
for it. Every extra clause is a clause read in the cold.

**The shelf gains an orientation line.** One line, above the items,
**unconditional** — shown whether or not the reader holds a session.

Session state is not a proxy for understanding. Someone may scan the
take/leave card *precisely because* they are trying to work out what this
is; that scan mints a session, and hiding the explanation from them would
hide it from exactly the person asking. The line is short enough that a
steward re-reading it daily costs less than a confused visitor bouncing.

## 4. Type: consequence, not role

`.err` currently means two unrelated things: "this field is wrong" and "you
are about to destroy twelve secrets." They share a colour and nothing else.
That is why the rotate confirmation's most consequential sentence renders at
13px — errors were assumed short.

**Split them.** Field validation stays small. A new class carries
consequence, sized above body text. A sentence about an irreversible act is
never smaller than the body around it.

**Then set an explicit scale.** Four sizes are currently invented ad hoc
(12.5, 13, 15, 16). The scale replaces them so the next person writing a
consequential sentence does not have to guess:

| role | size |
|---|---|
| fine print — hints, footer, tags, row details | 13 |
| body, controls, form fields | 16 |
| item note | 17 (unchanged) |
| **consequence — irreversible, read this** | **19, semibold** |
| h2 | 21 (unchanged) |
| h1 | 27 (unchanged) |

`12.5` and `15` disappear. The serif faces on `.note`, `h1` and `h2` are
already deliberate and stay.

**Consequence is 19, not 17.** One or two pixels over body text is a
difference nobody sees, and a class that is meant to stop someone has to
actually stop them. 19 with semibold weight sits between the item note and
`h2` — clearly heavier than the paragraph around it, without becoming a
heading and stealing the page's structure. It keeps the error colour, so it
reads as a warning rather than as emphasis.

## 5. Control placement

**A destructive control never sits within a tap-target of a benign one, and
comes last.**

Today the tokens page ends with a full-width "Download the booklet" button
and, twelve pixels below it, "Start a new booklet…" — the two most different
actions on the page are the two closest together, on a surface specified for
one-handed outdoor use.

The rule applies everywhere: destructive actions group at the end, separated
by more space than anything else on the page separates, and never immediately
following the primary action.

## 6. Out of scope, with reasons

**Cold-cellular performance.** Already satisfied by construction: no
JavaScript, no images, no external requests, CSS inlined in the layout. A
page is a handful of kilobytes. The remaining win is compression, which
belongs to the reverse proxy §8 already assumes for TLS.

**The shelf sign's geometry.** Milestone 5.5, per §1.

**`boulevard booklet` reprint requiring `--base-url`.** Real, recorded during
the Milestone 4b acceptance run, and a genuine hazard — retyping a URL that
is printed on a mounted sign is how the permanent artifact acquires a typo.
But it is a CLI flag change, not look-and-feel. It rides with the `init` work
or stands alone.

**Rotation accumulating revoked rows** on the tokens page. Recorded; a
pruning policy is a behaviour change, not a sweep.

## 7. Testing

**Most of this is assertion, not behaviour.** The suite already pins copy in
several places, and `TestScanRevokedIsIndistinguishableFromUnknown` compares
two rendered bodies directly.

**Those assertions get updated, never relaxed.** The sloppy fix for a copy
sweep is to loosen an assertion to `strings.Contains(body, "card")` so it
stops caring. Every test that breaks gets its expected string rewritten to
the new copy, and any test that is weakened rather than updated is a defect.

**Two new guards:**

1. No user-facing template contains "box", "box door", or "Little Free
   Library". A test over the embedded template FS, so a future page cannot
   quietly reintroduce the vocabulary.
2. The orientation line renders on a shelf **with** a session and **without**
   one, and on an empty shelf and a stocked one — four cases, because
   "unconditional" is the whole point and a conditional is the obvious way to
   get it wrong.

**The golden PDF changes exactly once**, for the booklet cover's install
copy, and is inspected by eye before the fixture is re-recorded. It is the
one artifact in this milestone that gets printed and cannot be revised
afterward.

**The indistinguishability constraint survives the sweep.** The generic
failure page's copy may change, but it must go on rendering identically for
an unknown secret and a revoked one. That test compares bodies, so it will
catch a divergence — but the copy pass must not be tempted to make the
revoked case friendlier.
