# Feature Request Issue Template

When planning a new feature, the output is a GitHub issue that serves as the source of truth
for implementation, QA validation, and future reference. Every feature issue MUST follow this
template. Do not skip sections — if a section doesn't apply, write "N/A" with a brief reason.

## Principles

- **No code, only requirements.** The issue describes what the system should do, not how to
  build it. No file paths, function names, API routes, or implementation details.
- **Testable from the outside.** User stories must be verifiable by someone who has never
  seen the codebase — just a browser and the testmail inbox.
- **User intent first.** Every feature exists because a real person has a real problem. Start
  there. If you can't articulate the pain, you don't understand the feature yet.
- **Explicit scope boundaries.** What you're NOT building is as important as what you are.
  Unstated non-requirements become scope creep.

## Template

### User Intent

Describe the real-world situation this feature addresses. Who is the user? What are they
doing when they encounter this problem? What does their environment look like? What are
they juggling? Why is the current state painful?

Write this as a narrative, not bullet points. The reader should feel the friction. This
section grounds every subsequent decision — if a requirement doesn't trace back to the
intent, question whether it belongs.

### User Stories

Testable scenarios written from the user's perspective. Each story should be independently
verifiable through browser automation and maps directly to a workflow in
`docs/validation/registry.json`.

For each story, include:

- **Narrative**: "As a [role], I [action] and [expected outcome]." One sentence.
- **Preconditions**: What must exist before the story can be exercised (test data, system
  state). These become the `preconditions` array in the validation registry.
- **Assertions**: Observable outcomes the tester checks — what appears on screen, what
  emails arrive, what changes. These become the `assertions` array in the registry.

Cover the happy path first, then error/edge cases. Number the stories sequentially.

### Requirements

The functional spec. Organize into logical sub-sections (e.g., by component or capability).
Use tables for structured data like error messages. Be specific enough to implement from
but don't prescribe architecture.

### Affected Surfaces

A table of every user-facing touchpoint that changes. This tells the implementer the blast
radius and tells QA where to look.

| Surface | Change |
|---|---|
| [page, email, table, nav element, etc.] | [what changes about it] |

Include new pages, modified pages, email templates, database tables, and navigation changes.

### Non-Requirements

Explicitly state what is out of scope. This prevents scope creep during implementation and
sets expectations for reviewers. Be specific — "no X" is better than "keep it simple."

### Open Questions

Decisions deliberately left for the implementer or for a future conversation. These are
things the feature owner considered but chose not to lock down yet. Each question should
be answerable without revisiting the entire design.

---

## Example: QR Code Scan-to-Checkin (#89)

Below is a complete feature issue following this template.

---

### User Intent

Staff working the front desk during class check-in are juggling multiple things at once —
greeting people as they walk in, answering questions about the space, handling walk-up
membership inquiries, checking out tools, and signing students into classes that are about
to start. Classes often begin at the same time, so a rush of 10-20 students may arrive in
a 5-minute window, all needing to be checked in before they head to the classroom.

The current flow requires staff to find the right class, scroll through a registration list,
locate the student's name, and click a check-in button — per student. This is slow,
error-prone (wrong student, wrong class), and demands the staff member's full visual
attention on the screen during the busiest moment of the shift.

The goal is a check-in flow where the staff member can pick up a USB scanner, point it at
the student's phone, and move on to the next person — no searching, no clicking, no class
selection. The system should do all the thinking. Staff attention should be on the people in
front of them, not the screen.

### User Stories

#### 1. Member registers and receives QR code

> As a member, I register for a class and receive a confirmation email that contains a QR
> code for check-in. I can also see the QR code on my member portal classes page.

**Preconditions:**
- A published class exists with available capacity
- A member account exists with email set to the testmail address

**Assertions:**
- Confirmation email contains a visible QR code image
- Navigating to `/member/classes` shows a QR code for the upcoming registration
- The QR code is unique to this registration

#### 2. Guest registers and receives QR code

> As a guest registered by a member, I receive a confirmation email at my guest email
> address that contains a QR code for check-in.

**Preconditions:**
- A published class exists with available capacity
- A member registers a guest with a testmail guest email address

**Assertions:**
- Guest's testmail inbox receives a confirmation email with a QR code image
- The QR code is unique to this guest registration (different from the member's)

#### 3. Staff scans a valid QR code and student is checked in

> As a staff member, I open the scan check-in page, scan a student's QR code with the USB
> scanner, and the student is checked in automatically. I see confirmation in the scan list
> and hear a success sound.

**Preconditions:**
- A class is scheduled for today with at least one confirmed registration
- The registration has a QR code token
- Staff is logged in

**Assertions:**
- Scanning the token checks the student in (status changes to "attended")
- The scan list shows the student's name, class title, and timestamp
- A success audio cue plays
- The page immediately returns to listening for the next scan

#### 4. Staff scans an already-checked-in QR code

> As a staff member, I scan a QR code for a student who has already been checked in. I see
> an error in the scan list and hear an error sound.

**Preconditions:**
- A registration that has already been checked in

**Assertions:**
- Scan list shows "Already checked in" with the original check-in time
- An error audio cue plays
- The page returns to listening for the next scan

#### 5. Staff scans an invalid QR code

> As a staff member, I scan a QR code that doesn't match any registration. I see an error
> and hear an error sound.

**Assertions:**
- Scan list shows "Invalid QR code"
- An error audio cue plays
- The page returns to listening for the next scan

#### 6. Staff scans a QR code for a class not scheduled today

> As a staff member, I scan a valid QR code for a class that isn't happening today. I see
> an error explaining why.

**Preconditions:**
- A registration exists for a class scheduled on a different day

**Assertions:**
- Scan list shows "Class is not scheduled for today"
- An error audio cue plays

#### 7. Staff scans a cancelled or waitlisted registration

> As a staff member, I scan a QR code for a registration that was cancelled or is still
> waitlisted.

**Assertions:**
- Cancelled: scan list shows "Registration was cancelled"
- Waitlisted: scan list shows "Registration is waitlisted, not yet confirmed"
- Error audio cue plays for both

### Requirements

#### QR Code Generation

- One QR code per registration (one person + one class = one code)
- A unique, non-guessable **signed token** is generated when a registration is created
  (individual registration, staff registration, or cart checkout)
- The token is stored on the registration record
- The QR code encodes the token value (or a short URL containing it)
- Generated for both **members and guests** — anyone with a confirmed registration gets
  a QR code

#### QR Code Delivery

- **Confirmation email**: QR code image embedded inline in the existing registration
  confirmation email
- **Member portal**: QR code visible on `/member/classes` for each upcoming registration
  (members only — guests don't have portal access)
- **Guest registrations**: QR code sent to the guest's email address directly

#### Staff Scan Page (`/staff/class-checkin`)

- **Universal scope**: handles any class happening today — no class selection step required
- **USB keyboard-emulation scanner support**: the page maintains a hidden auto-focused text
  input that captures the scanner's rapid keystrokes followed by Enter
- **Always listening**: after each scan result (success or error), focus immediately returns
  to the input with no staff interaction required
- **Audio feedback**: distinct success sound and error sound so staff notice results without
  watching the screen
- **Running scan list**: persistent scrolling log of all scans this session showing student
  name, class title, timestamp, and result (success or error with reason)

#### Error Cases

| Scenario | Message |
|---|---|
| Invalid or unknown token | "Invalid QR code" |
| Already checked in | "Already checked in" (show original check-in time) |
| Registration cancelled | "Registration was cancelled" |
| Class not scheduled today | "Class is not scheduled for today" |
| Registration is waitlisted | "Registration is waitlisted, not yet confirmed" |

### Affected Surfaces

| Surface | Change |
|---|---|
| `class_registrations` table | New column for QR token |
| Registration creation (all paths) | Generate and store signed token |
| Confirmation email template | Embed QR code image inline |
| `/member/classes` page | Display QR code per upcoming registration |
| `/staff/class-checkin` (new) | New staff scan page |
| Staff sidebar navigation | Add link to scan check-in page |

### Non-Requirements

- Camera-based QR scanning is out of scope — USB scanner only
- This does not replace the existing manual check-in flow (registration list + button) —
  both coexist
- No changes to the registration purchase flow itself — QR generation is additive

### Open Questions

- Should QR tokens expire (e.g., 24 hours after the class ends)?
- Should the scan page show a count of checked-in vs. total registrations per class today?
- Exact audio tones/files TBD — short, distinct, and non-intrusive
- Should the QR code encode a full URL (e.g., `https://domain/checkin/{token}`) or just the
  raw token string?
