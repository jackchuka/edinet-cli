<div align="center">

# edinet-cli

**Japanese corporate filings from EDINET, in one static binary.**

Search filings by company name or ticker, download them in any format,<br>
and pull financial figures straight out of the XBRL data.

[![Test](https://github.com/jackchuka/edinet-cli/actions/workflows/test.yml/badge.svg)](https://github.com/jackchuka/edinet-cli/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/jackchuka/edinet-cli?sort=semver)](https://github.com/jackchuka/edinet-cli/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/jackchuka/edinet-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

<img src="assets/demo.gif" alt="edinet finding a company, listing its annual report, and extracting consolidated figures from the XBRL" width="800">

[Installation](#installation) · [Quick Start](#quick-start) · [Commands](#commands) · [Using with J-Quants](#using-with-j-quants) · [How It Works](#how-it-works)

</div>

---

[EDINET](https://disclosure2.edinet-fsa.go.jp/), run by Japan's Financial Services Agency, holds every statutory filing from every listed Japanese company, and its API is free. But the API offers no conveniences. It has no company search, because every query is keyed by EDINET code. Its list endpoint takes exactly one date, so a quarter means ninety requests. It reports errors inside HTTP 200 responses. Its "CSV" downloads are really UTF-16LE tab-separated files inside a ZIP. Most existing clients need Python.

**edinet-cli** handles all of that, so a question like "what was Nintendo's consolidated operating income last year?" is two commands.

## Features

- **Search by name or ticker**: `--company 任天堂` or `--sec-code 7974`, backed by the FSA code list, which is downloaded and cached for you
- **Date ranges**: `--from`/`--to` fans out one request per day, with bounded concurrency and backoff on rate limits
- **Local filters**: filter by document type (`--type yuho`), by filer, or by the file formats a filing actually offers
- **XBRL facts**: `edinet facts` decodes the CSV bundle into rows you can grep, filter by 連結/個別 and period, and pipe onward
- **Joinable output**: every fact row carries its filing's document ID, codes, fiscal period and accounting standard
- **Scriptable**: every command supports `-o table`, `-o json` and `-o csv`
- **Single binary**: no Python, no venv, no runtime dependencies

## Installation

### Homebrew

```bash
brew install jackchuka/tap/edinet
```

### Go

```bash
go install github.com/jackchuka/edinet-cli/cmd/edinet@latest
```

### Binary

Download a prebuilt binary from the [releases page](https://github.com/jackchuka/edinet-cli/releases).

## Quick Start

1. **Get an API key**: it's free from the FSA at <https://api.edinet-fsa.go.jp/api/auth/index.aspx>. Then set it:

   ```bash
   export EDINET_API_KEY=your-key-here
   ```

   Every command also accepts `--api-key`. The key is never written to disk.

2. **Find a filing**:

   ```bash
   edinet docs list --from 2026-06-20 --to 2026-06-30 --type yuho --company 任天堂
   ```

   ```console
   DOCID     SUBMITTED         EDINET  TICKER  FILER           TYPE            DESCRIPTION                               FILES
   S100Y9NX  2026-06-25 15:30  E02367  7974    任天堂株式会社  有価証券報告書  有価証券報告書－第86期(2025/04/01－2026…  main,pdf,attach,csv
   ```

3. **Pull figures out of it**:

   ```bash
   edinet facts S100Y9NX --element NetSales --consolidated --period 当期
   ```

   ```console
   ELEMENT             LABEL   PERIOD  SCOPE  VALUE          UNIT
   jppfs_cor:NetSales  売上高  当期    連結   2313051000000  円
   ```

## Commands

| Command                         | Description                                                      |
| ------------------------------- | ---------------------------------------------------------------- |
| `edinet docs list`              | List filings submitted on a date or over a date range            |
| `edinet docs get <docID>`       | Download one rendition of a filing                               |
| `edinet facts [docID...]`       | Extract reported values from filings' CSV bundles                |
| `edinet company search <query>` | Find a company by name, ticker, EDINET code, or corporate number |
| `edinet company sync`           | Refresh the cached EDINET code list                              |
| `edinet codes`                  | Print the document type and ordinance code tables, plus aliases  |
| `edinet version`                | Print version information                                        |

Global flags:

| Flag            | Default           | Description                                 |
| --------------- | ----------------- | ------------------------------------------- |
| `--api-key`     | `$EDINET_API_KEY` | EDINET API key                              |
| `--concurrency` | `3`               | Max parallel requests when fetching a range |

### `docs list`: find filings

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

| Flag                  | Default | Description                                                                  |
| --------------------- | ------- | ---------------------------------------------------------------------------- |
| `--date`              | today   | Single filing date (`YYYY-MM-DD`); cannot be combined with `--from`/`--to`   |
| `--from`, `--to`      |         | Inclusive date range; `--to` defaults to today, and `--to` requires `--from` |
| `--company`           |         | Name, ticker, EDINET code, or corporate number                               |
| `--edinet-code`       |         | Exact EDINET code, e.g. `E02144`                                             |
| `--sec-code`          |         | Securities code / ticker, e.g. `7203`                                        |
| `--type`              |         | Document type by code (`120`) or alias (`yuho`); repeatable                  |
| `--has`               |         | Only filings offering these files: `main`, `pdf`, `attach`, `english`, `csv` |
| `--include-withdrawn` | `false` | Include withdrawn filings and withdrawal notices                             |
| `--include-expired`   | `false` | Include filings whose viewing period has expired                             |
| `--limit`             | `0`     | Stop after N results (`0` = no limit)                                        |
| `-o`, `--output`      | `table` | `table`, `json`, or `csv`                                                    |

### `docs get`: download a filing

```bash
edinet docs get S100AAAA                                  # main document + XBRL (zip)
edinet docs get S100AAAA --file pdf                       # PDF
edinet docs get S100AAAA --file csv --extract --out ./dl  # CSV bundle, unpacked
edinet docs get S100AAAA --file pdf --stdout > report.pdf
```

| Flag           | Default | Description                               |
| -------------- | ------- | ----------------------------------------- |
| `-f`, `--file` | `main`  | Which rendition to download (table below) |
| `--out`        | `.`     | Directory to write to                     |
| `--extract`    | `false` | Unpack the archive instead of saving it   |
| `--stdout`     | `false` | Write the file to stdout                  |
| `--force`      | `false` | Overwrite existing files                  |

| `--file`  | Contents                                         |
| --------- | ------------------------------------------------ |
| `main`    | Submitted document, audit report, and XBRL (zip) |
| `pdf`     | The PDF shown on the EDINET viewer               |
| `attach`  | Substitute and attached documents (zip)          |
| `english` | English-language filings (zip)                   |
| `csv`     | XBRL rendered to CSV (zip)                       |

### `facts`: extract financial figures

`edinet facts` downloads filings' CSV bundles, decodes them, and turns them into rows you can filter. Pass document IDs, use `-` to read IDs from stdin, or use `--file` to read bundles already on disk. Only filings whose `csv` flag is set have a bundle, and `edinet docs list --has csv` finds them.

```bash
# Revenue lines from one annual report
edinet facts S100AAAA --grep 売上高

# Several filings at once
edinet facts S100AAAA S100BBBB --element NetSales -o csv

# A whole month of annual reports, piped in
edinet docs list --from 2026-06-01 --to 2026-06-30 --type yuho --has csv -o json \
  | jq -r '.[].docID' \
  | edinet facts - --element NetSales --consolidated -o csv

# Bundles already on disk
edinet facts --file ./downloads/*_csv.zip -o json
```

| Flag                              | Default | Description                                         |
| --------------------------------- | ------- | --------------------------------------------------- |
| `--grep`                          |         | Keep facts whose label or value contains this text  |
| `--element`                       |         | Keep facts whose XBRL element ID contains this text |
| `--consolidated` / `--standalone` | `false` | Keep 連結 or 個別 facts only                        |
| `--period`                        |         | Keep facts from this relative period, e.g. `当期`   |
| `--numeric`                       | `false` | Drop narrative text blocks                          |
| `--file`                          |         | Read a downloaded CSV bundle instead (repeatable)   |
| `--limit`                         | `0`     | Stop after N facts (`0` = no limit)                 |
| `-o`, `--output`                  | `table` | `table`, `json`, or `csv`                           |

Behavior worth knowing:

- **Joinable rows**: every row in `json` and `csv` output carries the filing it came from (document ID, EDINET code, securities code, fiscal period, accounting standard), so you can join results against other data without a second lookup.
- **Partial failures**: downloads run `--concurrency` at a time, and output keeps the input order. A filing that can't be read is reported on stderr and skipped. The rest still come through, but the exit code is non-zero.
- **Empty stdin is not an error**: if `-` receives no IDs, the output is empty, so a quiet week upstream doesn't break a pipeline.
- **`--file` changes positional arguments**: once `--file` is given, positional arguments are read as more bundle paths, which is what makes `--file ./dl/*.zip` work. `docId` is recovered from the `<docID>_csv.zip` filename that `docs get` writes, and a renamed bundle yields an empty `docId`.
- **IFRS and US GAAP filers**: their CSV bundles can label every fact as `その他` instead of 連結 or 個別, so `--consolidated` may return nothing for them. Check `accountingStandard` and drop the scope filter if needed.
- **Large batches**: `table` buffers every row to align columns, so use `-o csv` or `-o json` for those.

### `company`: look up companies

```bash
edinet company search トヨタ
edinet company search 7203
edinet company search "sony group" -o json
edinet company sync            # refresh the cached code list
```

`company search` returns up to `--limit 20` matches by default (`0` = no limit).

### `codes`: reference tables

```bash
edinet codes
edinet codes -o json | jq '.[] | select(.table == "alias")'
```

These are the document type and ordinance code tables, plus the aliases accepted by `--type`. The aliases include `yuho` (有価証券報告書), `shihanki` (四半期報告書), `hanki` (半期報告書), `rinji` (臨時報告書), `naibu` (内部統制報告書), `tob` (公開買付届出書), `taryo` (大量保有報告書), and their `-teisei` correction variants. With `-o json` or `-o csv`, all three tables come out as one flat set of `table`/`key`/`value` records.

## Using with J-Quants

[J-Quants](https://jpx-jquants.com/) serves prices and normalised financial summaries for listed Japanese companies. It pairs well with EDINET, because the two cover different ground:

- **Item-level financial statements** (`/fins/details`) are on the Premium plan only. The same figures are in every annual report's XBRL, which `edinet facts` reads for free.
- **The Free plan delays everything by 12 weeks.** EDINET serves filings on the day they're submitted.
- **Narrative sections, 臨時報告書, TOB filings, and internal control reports** aren't in J-Quants at any tier.
- **決算短信 comes from TDnet, not EDINET.** For headline earnings ahead of the annual report, J-Quants `/fins/summary` is the right source.

Joining the two needs no translation:

- `secCode` is EDINET's 5-digit code with its trailing zero (Toyota is `72030`), the same form J-Quants uses for `Code`. The `json` and `csv` output keeps it as is. Only the table display trims it to 4 digits.
- `fiscalYearEnd` and `periodEnd` line up with the fiscal year end and current period end J-Quants reports. They differ in a semiannual report, which is why both are given.
- `accountingStandard` tells you whether an element ID follows Japan GAAP, IFRS, or US GAAP. This tool deliberately keeps no mapping between them, so element IDs are reported as filed.

```bash
# Every annual report filed in June, one row per reported revenue figure
edinet docs list --from 2026-06-01 --to 2026-06-30 --type yuho --has csv -o json \
  | jq -r '.[].docID' \
  | edinet facts - --element NetSales --consolidated --period 当期 -o csv \
  > netsales.csv
```

## How It Works

The tool talks to two upstream sources:

- **The API** (`api.edinet-fsa.go.jp/api/v2`) serves the document list and downloads. Ranges are fetched one request per day, up to `--concurrency` at a time, and rate-limited requests are retried with exponential backoff. The API can report a failure inside an HTTP 200 response, so every response body is checked, and downloads are checked by `Content-Type` before anything reaches disk.
- **The code list** (`Edinetcode.zip`) is a separate Shift-JIS download covering about 11,000 filers. It is cached under your user cache directory, refreshed weekly, and backs `--company` and `edinet company search`.

A few other details:

- **Tokyo dates**: all dates are read as Tokyo dates, whatever your machine's timezone. With no flags, `edinet docs list` asks for today in Japan.
- **10-year limit**: EDINET keeps 10 years of filings, and older dates are rejected before any request is sent.
- **Ticker format**: both `7203` and `72030` work anywhere a ticker is accepted.

## Development

```bash
go test -race ./...
go build ./cmd/edinet
vhs assets/demo.tape   # regenerate the demo (needs EDINET_API_KEY)
```

The tests use `httptest` and fixtures throughout, and nothing in the suite touches the live API.

## License

[MIT](LICENSE)
