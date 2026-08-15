package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackchuka/edinet-cli/internal/codelist"
	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

func (g *globals) client() (*edinet.Client, error) {
	key, err := g.key()
	if err != nil {
		return nil, err
	}
	return edinet.New(key), nil
}

// addFormatFlag registers -o on a command and returns a pointer to its value.
func addFormatFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "output", "o", "table",
		fmt.Sprintf("output format: %s", joinOr(output.Formats())))
}

func joinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	out := ""
	for i, s := range items {
		switch {
		case i == 0:
			out = s
		case i == len(items)-1:
			out += ", or " + s
		default:
			out += ", " + s
		}
	}
	return out
}

// loadCodeList returns the cached EDINET code list, downloading it when needed.
// The download is announced on stderr because it is a surprising pause the
// first time someone passes --company.
func loadCodeList(ctx context.Context) ([]codelist.Entry, error) {
	store, err := codelist.NewStore()
	if err != nil {
		return nil, err
	}
	if _, cached := store.Age(); !cached {
		fmt.Fprintln(os.Stderr, "Downloading the EDINET code list (one-time, ~1MB)...")
	}
	return store.Load(ctx, codelist.DefaultMaxAge)
}

// parseDay accepts a YYYY-MM-DD date, read as a Tokyo date.
func parseDay(s string) (time.Time, error) {
	d, err := edinet.ParseDate(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", s)
	}
	return d, nil
}

// dateRange turns the --date / --from / --to flags into an inclusive range.
// With none of them set, the range is today, matching EDINET's own model of one
// request per filing day.
//
// now is resolved in Tokyo, so the default day matches the one EDINET is
// filing against rather than the one on the caller's wall clock.
func dateRange(date, from, to string, now time.Time) (time.Time, time.Time, error) {
	jst := now.In(edinet.JST)
	today := time.Date(jst.Year(), jst.Month(), jst.Day(), 0, 0, 0, 0, edinet.JST)

	if date != "" {
		if from != "" || to != "" {
			return time.Time{}, time.Time{}, fmt.Errorf("--date cannot be combined with --from or --to")
		}
		d, err := parseDay(date)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return d, d, nil
	}

	if from == "" && to == "" {
		return today, today, nil
	}

	start, end := today, today
	var err error
	if from != "" {
		if start, err = parseDay(from); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if to != "" {
		if end, err = parseDay(to); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	// --to alone reads as "everything up to here"; without a start that would be
	// ten years of requests, so require --from to be explicit about it.
	if from == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("--to needs a --from (EDINET is queried one day at a time)")
	}
	return start, end, nil
}
