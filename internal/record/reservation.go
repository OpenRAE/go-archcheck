package record

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A reservation is an annotated tag refs/tags/design/NNNN whose message is
// "issue: N". It keeps the design number NNNN for the issue N.
var (
	reservationRefPattern = regexp.MustCompile(`^refs/tags/design/([0-9]{4})$`)
	reservationSubject    = regexp.MustCompile(`^issue: ([1-9][0-9]*)$`)
)

// ReservationTag returns the tag name that reserves number.
func ReservationTag(number int) string {
	return fmt.Sprintf("design/%04d", number)
}

// ReservationMessage returns the tag message that reserves a number for issue.
func ReservationMessage(issue int) string {
	return "issue: " + strconv.Itoa(issue)
}

// ReservedNumber returns the design number of a full reservation tag
// reference, and false when ref is not one.
func ReservedNumber(ref string) (int, bool) {
	n, ok := submatchNumber(reservationRefPattern.FindStringSubmatch(ref))
	return n, ok && n > 0
}

// Number returns the design number in the file name of a record, and false
// when the name is not NNNN-slug.md.
func Number(name string) (int, bool) {
	return submatchNumber(namePattern.FindStringSubmatch(name))
}

// submatchNumber returns the first submatch of a pattern match as a number.
func submatchNumber(match []string) (int, bool) {
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	return n, err == nil
}

// ParseReservations parses a reservations file, as
// "git for-each-ref --format='%(refname)%09%(objecttype)%09%(contents:subject)'"
// writes it. It maps each number that an annotated tag reserves to the issue
// in the tag message, or to 0 when the message names no issue. A lightweight
// tag reserves no number. The result is never nil, so an empty file turns
// on the check with no reservations.
func ParseReservations(listing []byte) (map[int]int, error) {
	reservations := map[int]int{}
	lines := map[int]int{}
	scanner := bufio.NewScanner(bytes.NewReader(listing))
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if text == "" {
			continue
		}
		fields := strings.SplitN(text, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want the reference name, the object type and the subject, separated by tabs", line)
		}
		number, ok := ReservedNumber(fields[0])
		if !ok {
			return nil, fmt.Errorf("line %d: %q is not a reservation tag refs/tags/design/NNNN", line, fields[0])
		}
		if prior, seen := lines[number]; seen {
			return nil, fmt.Errorf("line %d: %s is also on line %d", line, fields[0], prior)
		}
		lines[number] = line
		if fields[1] == "tag" {
			issue, ok := submatchNumber(reservationSubject.FindStringSubmatch(fields[2]))
			if !ok {
				issue = 0
			}
			reservations[number] = issue
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the reservations: %w", err)
	}
	return reservations, nil
}

// reservationDefect returns the defect of the reservation of a record with
// number and issue, or "" when the reservation matches or the check is off.
func reservationDefect(reservations map[int]int, number, issue int) string {
	if reservations == nil {
		return ""
	}
	tag := ReservationTag(number)
	reserved, ok := reservations[number]
	if !ok {
		return fmt.Sprintf("design number %d has no reservation tag %s", number, tag)
	}
	if reserved == issue {
		return ""
	}
	names := "no issue"
	if reserved != 0 {
		names = "issue " + strconv.Itoa(reserved)
	}
	return fmt.Sprintf("the reservation tag %s names %s, but the front matter states issue %d", tag, names, issue)
}
