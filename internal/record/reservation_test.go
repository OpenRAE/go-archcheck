package record

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/OpenRAE/go-rae-archcheck/internal/finding"
)

func TestReservationFormat(t *testing.T) {
	if got := ReservationTag(4); got != "design/0004" {
		t.Errorf("ReservationTag(4) = %q, want design/0004", got)
	}
	if got := ReservationMessage(38); got != "issue: 38" {
		t.Errorf("ReservationMessage(38) = %q, want issue: 38", got)
	}
}

func TestReservedNumber(t *testing.T) {
	tests := []struct {
		ref    string
		number int
		ok     bool
	}{
		{ref: "refs/tags/design/0001", number: 1, ok: true},
		{ref: "refs/tags/design/9999", number: 9999, ok: true},
		{ref: "refs/tags/design/0000"},
		{ref: "refs/tags/design/001"},
		{ref: "refs/tags/design/00001"},
		{ref: "refs/tags/design/0001x"},
		{ref: "refs/tags/v0001"},
		{ref: "refs/heads/design/0001"},
	}
	for _, tc := range tests {
		number, ok := ReservedNumber(tc.ref)
		if number != tc.number || ok != tc.ok {
			t.Errorf("ReservedNumber(%q) = %d, %t, want %d, %t", tc.ref, number, ok, tc.number, tc.ok)
		}
	}
}

func TestNumber(t *testing.T) {
	if n, ok := Number("0012-slug.md"); n != 12 || !ok {
		t.Errorf("Number(0012-slug.md) = %d, %t, want 12, true", n, ok)
	}
	if n, ok := Number("0000-zero.md"); n != 0 || !ok {
		t.Errorf("Number(0000-zero.md) = %d, %t, want 0, true", n, ok)
	}
	if n, ok := Number("bad name.md"); n != 0 || ok {
		t.Errorf("Number(bad name.md) = %d, %t, want 0, false", n, ok)
	}
}

func TestParseReservations(t *testing.T) {
	listing := "refs/tags/design/0001\ttag\tissue: 3\n" +
		"\n" +
		"refs/tags/design/0002\ttag\tissue: 04\n" +
		"refs/tags/design/0003\ttag\tReserve the number\n" +
		"refs/tags/design/0004\tcommit\tissue: 5\n" +
		"refs/tags/design/0005\ttag\tissue: 6\twith a tab\n" +
		"refs/tags/design/0006\ttag\tissue: 99999999999999999999\n"
	got, err := ParseReservations([]byte(listing))
	if err != nil {
		t.Fatalf("ParseReservations() error = %v", err)
	}
	want := map[int]int{1: 3, 2: 0, 3: 0, 5: 0, 6: 0}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseReservations() mismatch (-want +got):\n%s", diff)
	}
}

func TestParseReservationsEmpty(t *testing.T) {
	got, err := ParseReservations(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ParseReservations(nil) = %v, %v, want an empty map that turns on the check", got, err)
	}
}

func TestParseReservationsErrors(t *testing.T) {
	tests := []struct {
		name, listing, want string
	}{
		{name: "two fields", listing: "refs/tags/design/0001\ttag\n", want: "line 1: want the reference name, the object type and the subject"},
		{name: "spaces", listing: "refs/tags/design/0001 tag issue: 1\n", want: "line 1: want the reference name"},
		{name: "other ref", listing: "refs/tags/design/0001\ttag\tissue: 1\nrefs/tags/design/next\ttag\tissue: 1\n", want: `line 2: "refs/tags/design/next" is not a reservation tag refs/tags/design/NNNN`},
		{name: "zero", listing: "refs/tags/design/0000\ttag\tissue: 1\n", want: "is not a reservation tag"},
		{name: "duplicate", listing: "refs/tags/design/0001\ttag\tissue: 1\nrefs/tags/design/0001\tcommit\tissue: 1\n", want: "line 2: refs/tags/design/0001 is also on line 1"},
		{name: "long line", listing: "refs/tags/design/0001\ttag\t" + strings.Repeat("x", 70000) + "\n", want: "read the reservations"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReservations([]byte(tc.listing))
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != nil {
				t.Errorf("ParseReservations() = %v, %v, want an error that contains %q", got, err, tc.want)
			}
		})
	}
}

func TestCheckReservations(t *testing.T) {
	fsys := fstest.MapFS{
		"d/0001-start.md":    record(1, 3, "Start"),
		"d/0002-layers.md":   record(2, 4, "Layers"),
		"d/0003-other.md":    record(3, 5, "Other"),
		"d/0004-noissue.md":  record(4, 9, "No issue in tag"),
		"d/0005-invalid.md":  record(5, 0, "Invalid"),
		"d/0006-mismatch.md": record(7, 6, "Mismatch"),
	}
	reservations := map[int]int{1: 3, 3: 8, 4: 0, 5: 1, 6: 1}
	got, err := Check(fsys, "d", reservations)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	const fix = "Reserve the design number for the issue of the record with archcheck reserve, and fetch the reservation tags before the check."
	want := []finding.Finding{
		{Location: "d/0002-layers.md", Rule: finding.Record, Message: "design number 2 has no reservation tag design/0002", Fix: fix},
		{Location: "d/0003-other.md", Rule: finding.Record, Message: "the reservation tag design/0003 names issue 8, but the front matter states issue 5", Fix: fix},
		{Location: "d/0004-noissue.md", Rule: finding.Record, Message: "the reservation tag design/0004 names no issue, but the front matter states issue 9", Fix: fix},
		{Location: "d/0005-invalid.md", Rule: finding.Record, Message: "the front matter needs an issue number and a title", Fix: recordFix},
		{Location: "d/0006-mismatch.md", Rule: finding.Record, Message: "the front matter states design 7, but the file name states 6", Fix: recordFix},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Check() mismatch (-want +got):\n%s", diff)
	}
}

func TestCheckReservationsEmpty(t *testing.T) {
	fsys := fstest.MapFS{"d/0001-start.md": record(1, 3, "Start")}
	got, err := Check(fsys, "d", map[int]int{})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(got) != 1 || got[0].Message != "design number 1 has no reservation tag design/0001" {
		t.Errorf("Check() = %v, want one missing reservation", got)
	}
}
