# edinet-cli

[![Test](https://github.com/jackchuka/edinet-cli/actions/workflows/test.yml/badge.svg)](https://github.com/jackchuka/edinet-cli/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/jackchuka/edinet-cli?sort=semver)](https://github.com/jackchuka/edinet-cli/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A single-binary CLI for [EDINET](https://disclosure2.edinet-fsa.go.jp/), the Japanese Financial Services Agency's corporate disclosure system — search filings by company name or ticker, download them, and pull financial figures out of the XBRL data.

```console
$ edinet docs list --from 2026-06-20 --to 2026-06-30 --type yuho --company トヨタ
DOCID     SUBMITTED         EDINET  TICKER  FILER                 TYPE            DESCRIPTION              FILES
S100XXXX  2026-06-24 15:02  E02144  7203    トヨタ自動車株式会社  有価証券報告書  有価証券報告書－第122期  main,pdf,csv

$ edinet facts S100XXXX --grep 売上高 --consolidated --period 当期
ELEMENT             LABEL   PERIOD  SCOPE  VALUE           UNIT
jpcrp_cor:NetSales  売上高  当期    連結   45095325000000  円
```

## Why edinet-cli?

EDINET holds every statutory filing from every listed Japanese company, and its API is free. But the API is two endpoints and no conveniences, so getting data out of it usually means writing a scraper or paying someone who already did:

- **No company search** — every query is keyed by EDINET code, and the name-to-code mapping lives in a separate Shift-JIS ZIP with a junk row above the header.
- **No date ranges** — the list endpoint accepts exactly one date, so a quarter means ninety requests.
- **Errors hide inside HTTP 200** — the status line says OK and the real status sits in the response body, so naive clients silently treat failures as success.
- **"CSV" downloads aren't CSV** — they are UTF-16LE tab-separated files inside a ZIP, which turn to mojibake in anything that trusts the extension.
- **Every existing tool needs Python** — a venv and a dependency tree to answer one question.

**edinet-cli** handles all of that behind a single static binary:

- **Search by name or ticker** — `--company トヨタ` or `--sec-code 7203`, with the code list downloaded and cached for you.
- **Ranges just work** — `--from`/`--to` fans out day by day with bounded concurrency and backoff on rate limits.
- **Filter locally** — by document type, filer, or which file formats a filing actually offers.
- **Read the XBRL** — `edinet facts` decodes the CSV bundle into rows you can grep, filter by 連結/個別, and pipe onward.
- **Scriptable output** — every command supports `-o json` and `-o csv`.

## Installation

### Homebrew

```bash
brew install jackchuka/tap/edinet
```

### Go

```bash
go install github.com/jackchuka/edinet-cli/cmd/edinet@latest
```

Or download a binary from the [releases page](https://github.com/jackchuka/edinet-cli/releases).

## Setup

Get a free API key from the FSA at <https://api.edinet-fsa.go.jp/api/auth/index.aspx>, then:

```bash
export EDINET_API_KEY=your-key-here
```

Every command accepts `--api-key` as an override. The key is never written to disk.

## Usage

### Find filings

```bash
# What was filed today
edinet docs list

# Annual reports filed during a quarter
edinet docs list --from 2026-04-01 --to 2026-06-30 --type yuho

# Everything one company filed last month
edinet docs list --from 2026-07-01 --to 2026-07-31 --company トヨタ

# By ticker, only filings that ship a CSV bundle
edinet docs list --date 2026-08-14 --sec-code 7203 --has csv

# Pipe to jq
edinet docs list --from 2026-08-01 --to 2026-08-07 -o json | jq -r '.[].docDescription'
```

Withdrawn filings and filings past their viewing period are hidden unless you pass `--include-withdrawn` or `--include-expired`.

| Flag | Description |
| --- | --- |
| `--date` | Single filing date (default today) |
| `--from`, `--to` | Inclusive date range |
| `--company` | Name, ticker, EDINET code, or corporate number |
| `--edinet-code`, `--sec-code` | Exact code match |
| `--type` | Document type by code (`120`) or alias (`yuho`), repeatable |
| `--has` | Only filings offering these files: `main`, `pdf`, `attach`, `english`, `csv` |
| `--limit` | Stop after N results |
| `-o`, `--output` | `table`, `json`, or `csv` |

### Download filings

```bash
edinet docs get S100XXXX                                  # main document + XBRL (zip)
edinet docs get S100XXXX --file pdf                       # PDF
edinet docs get S100XXXX --file csv --extract --out ./dl  # CSV bundle, unpacked
edinet docs get S100XXXX --file pdf --stdout > report.pdf
```

| `--file` | Contents |
| --- | --- |
| `main` | Submitted document, audit report, and XBRL (zip) |
| `pdf` | The PDF shown on the EDINET viewer |
| `attach` | Substitute and attached documents (zip) |
| `english` | English-language filings (zip) |
| `csv` | XBRL rendered to CSV (zip) |

### Extract financial facts

`edinet facts` downloads filings' CSV bundles, decodes them, and turns them into
rows you can filter. Every row in the `json` and `csv` output carries the filing
it came from — document ID, EDINET code, securities code, fiscal period,
accounting standard — so results can be joined against other data without a
second lookup.

Only filings whose `csv` flag is set have a bundle — find them with
`edinet docs list --has csv`.

```bash
# Revenue lines from one annual report
edinet facts S100XXXX --grep 売上高

# Several filings at once
edinet facts S100XXXX S100YYYY --element NetSales -o csv

# A whole month of annual reports, piped in
edinet docs list --from 2026-06-01 --to 2026-06-30 --type yuho --has csv -o json \
  | jq -r '.[].docID' \
  | edinet facts - --element NetSales --consolidated -o csv

# Bundles already on disk
edinet facts --file ./downloads/*_csv.zip -o json
```

Downloads run `--concurrency` at a time and output keeps the input order. A
filing that cannot be read is reported on stderr and skipped; the rest still
come through, and the exit code is non-zero so a script can tell. Empty input
on `-` (no document IDs on stdin) is not an error — it produces empty output,
so a quiet week in an upstream `docs list` doesn't break the pipeline.

Once `--file` is given, any positional arguments are read as further bundle
paths rather than document IDs — that's what makes `--file ./dl/*.zip` work,
with the shell expanding the glob. The `docId` column is recovered from the
filename `docs get` writes (`<docID>_csv.zip`); a renamed bundle yields an
empty `docId` rather than a guess.

The `table` format buffers every row to align its columns, so use `-o csv` or
`-o json` for large batches.

| Flag | Description |
| --- | --- |
| `--grep` | Match the label or value |
| `--element` | Match the XBRL element ID (substring, case-insensitive) |
| `--consolidated` / `--standalone` | Keep 連結 or 個別 figures |
| `--period` | Match the relative period, e.g. `当期` |
| `--numeric` | Drop narrative text blocks |
| `--file` | Read a downloaded bundle instead (repeatable) |

### Look up companies

```bash
edinet company search トヨタ
edinet company search 7203
edinet company search "sony group" -o json
edinet company sync            # refresh the cached code list
```

### Reference tables

```bash
edinet codes           # document type and ordinance codes, plus --type aliases
```

Aliases include `yuho` (有価証券報告書), `hanki` (半期報告書), `rinji` (臨時報告書), `taryo` (大量保有報告書), `naibu` (内部統制報告書), and `tob` (公開買付届出書).

`-o json` and `-o csv` emit the three tables as one flat set of records with
`table`, `key`, and `value` fields, e.g. `edinet codes -o json | jq '.[] | select(.table == "alias")'`.

## Using with J-Quants

[J-Quants](https://jpx-jquants.com/) serves prices and normalised financial
summaries for listed Japanese companies. It pairs well with EDINET, because the
two cover different ground:

- **Item-level financial statements** (`/fins/details`) are on the Premium plan
  only. The same figures are in every annual report's XBRL, which `edinet facts`
  reads for free.
- **The Free plan delays everything by 12 weeks.** EDINET serves filings the day
  they are submitted.
- **Narrative sections, 臨時報告書, TOB filings, and internal control reports**
  are not in J-Quants at any tier.
- **決算短信 comes from TDnet, not EDINET.** For headline earnings ahead of the
  annual report, J-Quants `/fins/summary` is the right source.

Joining the two needs no translation:

- `secCode` is EDINET's 5-digit code with its trailing zero (Toyota is `72030`),
  which is the same form J-Quants uses for `Code`. The `json` and `csv` output
  keeps it unmodified; only the table display trims it to 4 digits.
- `fiscalYearEnd` and `periodEnd` line up with the fiscal year end and current
  period end J-Quants reports. They differ in a semiannual report, which is why
  both are given.
- `accountingStandard` tells you whether an element ID follows Japan GAAP, IFRS,
  or US GAAP. This tool keeps no mapping between them on purpose — the element
  IDs are reported as filed.

```bash
# Every annual report filed in June, one row per reported revenue figure
edinet docs list --from 2026-06-01 --to 2026-06-30 --type yuho --has csv -o json \
  | jq -r '.[].docID' \
  | edinet facts - --element NetSales --consolidated --period 当期 -o csv \
  > netsales.csv
```

## How it works

Two upstream sources, both handled transparently:

- **The API** (`api.edinet-fsa.go.jp/api/v2`) serves the document list and downloads. Because its list endpoint takes a single date, ranges are fetched one request per day, up to `--concurrency` (default 3) at a time. Rate-limited requests retry with exponential backoff. Because it reports failures inside HTTP 200 responses, every body is inspected rather than trusted, and downloads are validated by `Content-Type` before anything reaches disk.
- **The code list** (`Edinetcode.zip`) is a separate Shift-JIS download of ~11,000 filers, cached under your user cache directory and refreshed weekly. It backs `--company` and `edinet company search`.

Other behavior worth knowing:

- All dates are Tokyo dates, whatever your machine's timezone. `edinet docs list` with no flags asks for today in Japan, so it returns the same filings from Osaka, London, or a UTC CI runner.
- EDINET keeps 10 years of filings. Older dates are rejected before a request is sent.
- `secCode` in EDINET is 5 digits with a trailing zero (Toyota is `72030`). Both `7203` and `72030` work anywhere a ticker is accepted.

## Development

```bash
go test -race ./...
go build ./cmd/edinet
```

Tests use `httptest` and fixtures throughout — nothing in the suite touches the live API.

## License

MIT
