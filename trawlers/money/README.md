# Money

Money archives financial accounts, balances, booked transactions and pending
activity. Collectors deliver one typed import contract; Money owns storage,
search and open. The CLI renders through `trawlkit/render`.

Use `trawl money accounts` for balances and complete collection intervals.
Use `trawl money transactions` for booked transactions, or add `--pending`.
Lists accept `--account`, `--after`, `--before`, `--date-basis`, `--matching`
and `--limit`. `--matching` uses the same text matching as search, before the
date, account and row limits. It returns individual movements for an agent to
inspect and calculate from; Money does not calculate merchant totals.
Account links come from the accounts list. Dates use `YYYY-MM-DD`; date basis
is `booked` or `charged`. A charge date used in place of an unavailable booking
date is marked in the list. A booking-date filter excludes records whose
booking date is unknown.

`trawl search` includes matching financial records alongside other sources.
Check `trawl money accounts` before concluding a payment was not made. A missing
search match does not establish that the account and period were collected.
`trawl open` shows the signed amount, account, source dates and supplied payment
details. Positive amounts enter the account; card charges are negative.
Amounts remain exact decimals. macOS uses the computer's currency and number
preferences; other platforms display the ISO currency code and exact decimal.

## Import contract

The schema is [money.proto](../../trawlkit/proto/trawl/money/money.proto).
An agent or provider adapter writes a completed directory under the Money
archive's `imports/` directory. It contains `import.txtpb`, a protobuf text
`trawl.money.MoneyImport`, and `originals/`, containing unchanged downloads.
Build under a directory whose name starts with a dot, then rename it to its
final unique name when complete. `trawl update money` applies completed imports
and retains them under `imports/applied/`.

Each import supplies its provider display name, actual collection timestamp
and account imports. Bind accounts to a full stable provider identity, or a
configured stable account identity. A masked suffix and a consent-session
account ID are not account identities. Each movement has an account-scoped
provider reference and namespace. Conversions of the same reference use the
same namespace. Without provider references, a collector must construct a
repeatable row identity from source fields that distinguish the actual rows.

The booked list has presence: omit it when booked activity was not read.
A complete pending list replaces the account's pending snapshot, including
when empty; omit it when pending was not read. Older pending observations
cannot replace newer ones. Never infer a pending-to-booked match.

Repeated observations replace populated fields in collection-time order;
absent fields preserve previously supplied detail. Parsed payment, flight and
hotel fields belong in their typed fields. Additional payment details contain
source facts not already represented there. Preserve unrepresented provider
metadata in the originals. No per-record source-file paths belong in records.

Declare complete history only after reading every page for that interval and
checking provider limits. The first and last returned transactions alone do
not establish completeness. Keep PDF-only history explicit in the availability
note. Balance dates describe the source's balance date, not download time.
Supply each balance's `balance_read_time` from its actual capture, separately
from transaction reads. Without a source as-of date, lists and open show that
read time. An account without a balance observation displays an unknown balance.

Each import applies in one database transaction. An invalid import stays in
place and fails the update through the normal run log; other valid imports can
still apply. Imports already applied do not create duplicate records. A fresh
Money database automatically replays retained imports in collection-time order,
with directory-name order breaking timestamp ties. Originals and imports are
source archives; the SQLite database is their rebuildable projection.

## Enable Banking collector

The optional `enable_banking` table in Money's `config.toml` supplies shared
`application_id` and `private_key_file` settings. Each
`[[enable_banking.connections]]` table supplies a unique, stable
`connection_identifier`, an absolute `session_file` path and a
`first_refresh_date`. Keep the connection identifier when renewing consent;
it identifies the read limit, not the rotating session. Credentials remain
outside the repository. Money never starts consent or signs in to a bank.

The collector supports ING and Wise. It reads each authorized session and
account details, pages through booked activity and reads balances. ING binds a
full IBAN to an imported account. Accounts without an IBAN can use configured
`[[enable_banking.connections.accounts]]` bindings: `identification_hash`
from Enable Banking and the imported `institution_identifier`,
`account_identifier_namespace` and `stable_account_identifier`. The provider's
identification hash matches an account across consents; a session account UID
does not. An unbound account fails the read rather than creating a duplicate.

Subsequent reads overlap the last successful booked read by seven days to
observe later postings and corrections. Wise also replaces the complete pending
snapshot, including an empty list. ING does not claim a pending read. Every
response is retained beside the typed import. Wise records the requested booked
date interval only after exhausting every continuation page, including empty
pages. Overlapping or adjacent intervals join imported statement coverage;
gaps remain separate. Coverage is current as of the displayed booked read time,
not a promise that no later posting can occur. ING retains its imported coverage.
Neither adapter infers completeness from the dates of returned movements.

Wise records fees separately from principal movements. Its entry reference
can recur for a debit and its reversal, so identity includes direction.
Historical imports must reconcile net statement amounts to the corresponding
principal and fee movements and use those same identities. Do not import a net
statement movement alongside its separate API principal and fee.

Enable Banking reads occur on `trawl update money` when configured. Bank read
limits apply; avoid using repeated live updates as a development test. Import
replay and rendering can run with the optional collector table absent.

Amex collection remains with an agent using the provider's website. That agent
writes this import contract; Money does not automate the website or acquire
bank/card credentials. Different provider collectors share the financial
contract, not their access or consent workflows.

Each configured connection attempts at most one read in each 24-hour interval,
including failed provider reads. Adding or renewing another connection does not
reset that interval. The timestamp is stored before the first request, so the
app's hourly updates and separate CLI invocations share the same limit.

Account imports may carry historical balances with their source dates. The
account list shows the latest balance for each basis; opening an account shows
all imported observations. A year-end balance does not establish transaction
coverage or a current balance. Portfolio value and cash are separate bases.
