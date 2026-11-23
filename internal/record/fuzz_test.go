package record

import "testing"

// FuzzParseFrontMatter checks that parseFrontMatter never panics, and that it
// returns the zero front matter when it reports no front matter.
func FuzzParseFrontMatter(f *testing.F) {
	for _, seed := range []string{"---\ndesign: 1\nissue: 1\ntitle: Start\n---\n", "---\n---\n", "", "---\ndesign: [\n---\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		meta, ok := parseFrontMatter(content)
		if !ok && meta != (frontMatter{}) {
			t.Fatalf("parseFrontMatter() = %+v, false, want the zero front matter", meta)
		}
	})
}

// FuzzParseReservations checks that ParseReservations never panics, that it
// returns a map exactly when it returns no error, and that each reserved
// number and issue is in range.
func FuzzParseReservations(f *testing.F) {
	for _, seed := range []string{"refs/tags/design/0001\ttag\tissue: 1\n", "refs/tags/design/0002\tcommit\tx\n\n", "", "refs/tags/design/0001\ttag\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, listing []byte) {
		got, err := ParseReservations(listing)
		if (err == nil) != (got != nil) {
			t.Fatalf("ParseReservations() = %v, %v, want a map or an error", got, err)
		}
		for number, issue := range got {
			if number < 1 || number > 9999 || issue < 0 {
				t.Fatalf("ParseReservations() maps %d to %d, want a number in 1-9999 and an issue of 0 or more", number, issue)
			}
		}
	})
}
