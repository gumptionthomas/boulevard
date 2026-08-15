# Milestone 1 — Presence

**Date:** 2026-08-14
**Status:** design approved, unbuilt
**Scope:** the scan endpoint, token validation with grace, the session cookie, and the banner — `DESIGN.md` §4, §6 and §12.

Milestone 0 produced printed cards. This milestone makes scanning one of them mean something.

---

## 1. What this milestone is

The first HTTP server. It turns a physical act — standing at the box and scanning the card inside its door — into a 24-hour session that later milestones will gate writes behind.

**Done means:** scan a card printed by Milestone 0 with a real phone, land on a real page, and see that the page knows you are at the box. Then scan an expired card and get told what is wrong instead of a failure.

## 2. Scope

A walking skeleton, not a shelf. The four routes in §5 exist and work end to end; the shelf page they lead to has no items, because items are Milestone 2. The point is that Milestone 2 adds items to a page that already exists, rather than building the page and the items together.

## 3. Non-goals

No items, no leave form, no take control, no steward admin, no approval queue, no images. No rate limiting — §4's 3 leaves / 3 takes per session cannot be enforced before leaves and takes exist (Milestone 3). No steward overrides (force-activate, extend, revoke) — those are Milestone 4; this milestone only *honors* a `revoked` state it never sets.

---

## 4. Decisions

| Decision | Choice | Why |
|---|---|---|
| M1 surface | Walking skeleton: `/`, `/b/:slug/`, `/b/:slug/about`, `/s/:token` | The banner needs a page to sit on, and the browse sign's URL must not 404. M2 then only adds items. |
| Session cookie | Opaque 128-bit random ID, **not signed** | §4 already requires a server-side session row, so the DB lookup is the check. A signature adds a key to store, rotate and lose, and defends against nothing the lookup doesn't. See §4.1. |
| Activation | Only ever moves forward | An older in-grace card must still grant a session, but must not rewind the steward's sense of which card is in the door. See §7. |
| Generic failure page | Knows nothing: no library name, no branding, no shelf link | Forced by the data model, not merely chosen. See §4.2. |
| Session expiry | Checked on read, never swept | A background sweeper over a table holding at most a handful of live rows is machinery with no purpose. |
| Banner time | Server's local time | The alternatives are worse; see §10.2. |

### 4.1 Why the session ID is not signed

`DESIGN.md` §4 says the cookie carries "a signed random session id" and that a server-side row records which token minted it. Those two halves pull against each other: if the session is stateful and the ID is looked up in SQLite on every request, a signature has no forgery left to prevent. Guessing a 128-bit random ID is infeasible, and the lookup already rejects anything never issued.

What signing *would* add is a key that has to live somewhere. Every option is worse than not having one:

- A key in the database is one more thing to generate, migrate and reason about, and it protects nothing the primary key doesn't.
- A key from a flag or environment variable becomes required install configuration, which §8 rejects outright: "If a config file is required, the install story has failed."

Not having a key also keeps §10's ejection story intact — a steward copies `fairview.db` to another host and nothing external is needed to make it work.

**This is a documented deviation from §4's wording.** The spec is amended in §12.

### 4.2 The generic failure page cannot know anything

§4 sends both an unknown secret and a revoked one to the same generic failure page. That reads as a security preference; it is actually forced.

Token secrets are unique host-wide (§10), which means **the token is how a request identifies its library**. An unknown secret therefore resolves to no library at all — there is no name to show and no shelf to link to, because we do not know which shelf.

A revoked secret *does* resolve. But rendering anything library-specific for a revoked token, while an unknown token renders a bare page, would confirm to whoever photographed a card that the secret was real. The generic page must therefore be the lowest common denominator of both cases, which is nothing.

The one link that is safe in both cases is the **host root**: in single-library mode it redirects to the sole shelf, and in a future multi-library host it shows the neighborhood map (§10). It never names a shelf, so it leaks nothing either way.

---

## 5. Routes

```
GET /                    302 → /b/{slug}/          (the sole library; a map in v2)
GET /b/{slug}/           the shelf shell
GET /b/{slug}/about      what Boulevard is, where this box is, who tends it
GET /s/{token}           validate → mint session → 302 → /b/{slug}/
```

**Two of these are not design choices.** Milestone 0 printed twelve cards encoding `{base_url}/s/{secret}` and a permanently-mounted sign encoding `{base_url}`. Those URLs exist on paper. `/s/:token` and `/` must work, and changing them invalidates a booklet.

`/b/:slug/` is the canonical form §10 requires; `/` redirecting to it is exactly what §10 permits for single-library mode. A request for a slug that does not exist is a 404 — not a redirect to the sole library, which would defeat the obligation.

**`/` when the database holds more than one library.** This is reachable today, not hypothetical: `boulevard booklet` run twice with different names creates two libraries in one file. The rule:

| Libraries in the database | `GET /` |
|---|---|
| Exactly one | 302 to `/b/{slug}/` |
| Zero | 404 |
| More than one | 404 |

The multi-library case gets a 404 rather than a list, because a page enumerating every shelf on a host is the neighborhood map, and that is a v2 host surface (§10) with its own design. Guessing at it here would be the wrong shape built early. Each library remains reachable at its own canonical `/b/{slug}/` regardless, so nothing is unreachable — only the convenience redirect is withheld.

`/s/:token` takes no slug because it does not need one: the token identifies its own library.

New command:

```
boulevard serve [--db boulevard.db] [--addr :8080]
```

---

## 6. Validation

A pure function — no I/O, no clock, no store:

```go
type Outcome int

const (
    Invalid Outcome = iota // unknown secret, or revoked
    NotYet                 // real token, its window plus grace has not opened
    Expired                // real token, past its window plus grace
    Granted                // mint a session
)

func Validate(tok boulevard.Token, today boulevard.Date, grace int) Outcome
```

The store resolves the secret first. A miss returns `Invalid` without calling this. A token whose state is `revoked` returns `Invalid`. Everything else compares `today` against `[valid_from − grace, valid_until + grace]` inclusive, with **grace = 7 days** per §4.

**The two sides of the window are separate outcomes.** An earlier draft of this spec had a single `OutOfWindow`, and §10.1 wrote only the expired copy — so a card scanned *before* its window was told it was "out of date" and that it "stopped working" on a date that has not happened. That case is reachable today: a neighbour scanning a spare card from the booklet, or a steward swapping a card more than seven days early, which `DESIGN.md` §4 anticipates under force-activate. Distinguishing them in `Validate` rather than in the handler keeps every comparison against the grace window in the one pure, boundary-tested place, and leaves the handler picking a page with no date arithmetic of its own.

`today` is a parameter, not a clock read. This is what makes the grace boundaries testable on the exact day, and an off-by-one at a seven-day edge is precisely the class of bug that ships silently and surfaces a month later as "the card stopped working early".

Cases that must have tests: the day before the window opens; the first day; the last day; the day after it closes; both grace edges from both directions; a revoked token inside its window; an unknown secret; and that too-early and too-late do not collapse into one outcome.

---

## 7. Activation moves forward only

§4 step 5 says: "On first valid scan, set `first_seen_at` and mark `active`. Expire any earlier `active` token." That is underdetermined in a case that will happen.

It is 3 September. The September card is active. Someone finds the August card — never scanned, because the steward forgot to put it up — and scans it. It is inside its 7-day grace, so §4 step 3 grants a session. It is also a *first* valid scan, so step 5 says activate it.

The rule this milestone implements:

1. On `Granted`, record `first_seen_at` if it is unset.
2. Advance `active` **only if** this token's `period_index` is greater than the current active token's (or there is no active token). When it advances, the previous active token becomes `expired`.
3. A token whose period is older than the current active one still grants a session and still records `first_seen_at`. It does not change which token is active.

Validation is driven by the date window and the revoked state, so `active` is largely diagnostic — it is what tells a steward which card should be in the door. That lowers the stakes but does not remove the need for a rule, and a stray old card found in a drawer must not be able to rewind it.

**This refines §4 step 5.** The spec is amended in §12.

---

## 8. Sessions

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    library_id  TEXT NOT NULL REFERENCES libraries(id),
    token_id    TEXT NOT NULL REFERENCES tokens(id),
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);
```

No index beyond the primary key. Every lookup is by `id`, which `PRIMARY KEY` already indexes, and since expiry is checked on read rather than swept, nothing ever queries by `expires_at`. An index on it would be exactly the dead weight removed from the `tokens` table in Milestone 0.

`id` is the cookie value: 128 bits from `crypto/rand`, Crockford base32, generated with the existing `boulevard.RandomBase32`.

`token_id` satisfies §4's "records which token minted it". It is what will let a steward revoke a photographed card in Milestone 4 and drop the sessions it minted along with it.

Cookie attributes:

| Attribute | Value |
|---|---|
| Name | `bl_session` |
| `HttpOnly` | yes |
| `SameSite` | `Lax` |
| `Path` | `/` |
| `Max-Age` | 86400 (24 hours, per §4) |
| `Secure` | set when the request arrived over TLS |

Expiry is checked on read: a session row whose `expires_at` has passed is treated as absent. No sweeper.

**Presence is attestable, not enforceable** (§4). A scanned URL can be texted to a friend. This milestone does not try to prevent that, and should not: rotation plus the rate limits of Milestone 3 bound the blast radius, and that is the whole intended defence.

---

## 9. Package layout

```
internal/web/      server.go     — construction, dependency wiring
                   routes.go     — the mux and its patterns
                   scan.go       — GET /s/{token}
                   shelf.go      — GET /b/{slug}/
                   about.go      — GET /b/{slug}/about
                   render.go     — template execution and error pages
                   templates/    — go:embed'd html/template files
internal/store/    session.go    — new: session create, lookup, delete one
                   token.go      — extended: TokenBySecret, forward-only activation
internal/tokens/   validate.go   — new: the pure Outcome function
cmd/boulevard/     serve.go      — new subcommand
```

`internal/web` owns HTTP and nothing else. Validation stays pure in `internal/tokens`. Persistence stays in `internal/store`, still library-scoped — **every new store method takes a `boulevard.LibraryID`, and there is still no ambient current-library value anywhere.**

Routing uses the standard library's `net/http` pattern matching (Go 1.22+). No router dependency: the four routes do not justify one, and every dependency has to earn its place against the single-static-binary promise.

Templates are `html/template` behind `go:embed`, with CSS inlined into the layout rather than served as a separate asset. One request, no round-trips — which is what "fast on cold cellular" actually costs (§6).

---

## 10. The pages

All four are read **one-handed, on a phone, outdoors, in bad light, possibly in winter** (§6). Large type, high contrast, no hover states, one obvious action per screen.

### 10.1 The three scan outcomes

**Granted** — 302 to `/b/{slug}/` with the cookie set. The shelf carries a persistent, quiet banner:

> **You're at the box.**
> You can leave or take until 4:12 PM tomorrow.

Naming a wall-clock deadline beats "24 hours" because it is what a person actually needs in order to decide whether to write the note now or at the kitchen table.

**Expired** — rendered at `/s/{token}`, deliberately **not** a failure page. §4 is explicit that this is diagnostic information the steward needs:

> **This card is out of date.**
> The card in this box is **August 2026**. It stopped working on 7 September.
> The steward needs to swap in the next card.
> Nothing is wrong with the shelf. You just can't leave or take until that happens.
>
> *[Browse the shelf anyway]*

Restating "August 2026" leaks nothing — it is printed on the card in the reader's hand. Naming *which* card comes next, and its position in the booklet, is deliberately omitted. The shelf link is present because here the token resolves and we genuinely know where to send them.

**NotYet** — the mirror image, also 200 and also not a failure. A card whose window has not opened is a real card in someone's hand, and the expired copy is actively wrong about it:

> **This card isn't in use yet.**
> The card you scanned is **July 2027**. It starts working on 24 June 2027.
> Until then, the card taped inside the box door is an earlier one.
> Nothing is wrong with the shelf. You just can't leave or take with this card yet.
>
> *[Browse the shelf anyway]*

The date named is `valid_from − grace`, the day the card actually begins working, mirroring the expired page's `valid_until + grace`. **A date in another calendar year carries its year**: card 12 of a booklet is nearly a year out, and "24 June" alone reads as a day that has already passed. Which card *is* currently in the door is still not named, for the same reason as above.

**Invalid** — no library name, no branding, no shelf link, per §4.2:

> **That code isn't valid.**
> It may be from an old booklet, or the link may have been copied rather than scanned.
> To leave or take something, scan the code inside the box door.
>
> *[Boulevard]* → the host root

### 10.2 The banner's timezone

The banner names a local wall-clock time, which needs a timezone. This milestone uses **the server's local time**, and assumes the host is configured for the box's timezone.

The alternatives are worse. A per-library timezone field is install configuration, which §8 rejects. Client-side conversion makes a core string depend on JavaScript. And the cost of being wrong is bounded: the deadline is approximate by nature, and a few hours of error in a 24-hour window changes nobody's behaviour.

This belongs in the install documentation.

### 10.3 The shelf shell

No items — those are Milestone 2. The page carries the library name, the banner when a session is live, and the leave control **visible but inert** with the §6 explanation when it is not: *"Scan the code at the box to take or leave something."* Visible-but-inert rather than hidden, so a remote reader understands the rule instead of thinking the feature is missing.

The empty state gets a first honest pass here — "The shelf is empty. Nothing here yet. That's an invitation." — but §12 assigns its real design attention to Milestone 5, and this milestone does not try to finish it.

---

## 11. Testing

**Pure unit tests — `internal/tokens`.** `Validate` at every boundary named in §6, plus revoked-inside-window and unknown-secret.

**Store — `internal/store`.** Session create, lookup, expired-lookup-returns-nothing, delete. Forward-only activation: an older in-grace token does not displace the active one; a newer token does and expires its predecessor; `first_seen_at` is recorded in both cases and never overwritten once set.

**Handlers — `internal/web`, via `httptest`.** The redirect target and cookie attributes on a granted scan; that `Secure` is set under TLS and absent without it; that an `Expired` response renders the library name and the out-of-date copy; that a `NotYet` response renders the not-yet-in-use copy with a year-bearing start date and never the word "expired"; that an `Invalid` response renders **no** library name and no shelf link; that `/` redirects to the canonical slug route; that an unknown slug is a 404 rather than a redirect.

Assertions target behaviour and structure, not markup — no golden HTML, because it would break on every wording change and teach the suite to be ignored.

**Manual.** Scan a real printed card from the Milestone 0 booklet with a phone, against a server started with `boulevard serve`. That is the only check that exercises the actual path from paper to page.

---

## 12. Amendments to DESIGN.md

1. **§4 session mechanics** — "a signed random session id" becomes an opaque random session id. The server-side row is the source of truth; a signature would add key management for no defence the lookup does not already provide. Rationale in §4.1.
2. **§4 validation step 5** — "On first valid scan, set `first_seen_at` and mark `active`" gains the forward-only qualifier: a token older than the current active one grants a session and records `first_seen_at`, but does not become active. Rationale in §7.
3. **§4 validation step 4** — "outside the window but otherwise valid" splits in two. Past the window plus grace is the card-is-out-of-date page §4 describes. *Before* it is a distinct page saying the card is not in use yet and naming the day it starts, because the out-of-date copy is wrong about a card that has not started. Rationale in §6 and §10.1.

---

## 13. Deferred to later milestones

- **Rate limits** (3 leaves, 3 takes per session) — Milestone 3, when leaves and takes exist.
- **Steward overrides** (force-activate, extend, revoke) — Milestone 4. This milestone honors `revoked` but never sets it.
- **The empty state's real design** — Milestone 5.
- **A second year's booklet.** Carried forward from Milestone 0: after twelve periods elapse, every token is outside its window plus grace, so every scan renders `Expired` and the shelf becomes read-only until the steward generates a new booklet. That is correct behaviour, and §4 already says "rotation past month 12: the steward generates a new booklet" — but nothing yet tells them to, and `boulevard booklet` currently reprints the same expired cards. Milestone 4 owns the fix.
