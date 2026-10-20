// Package resolver selects the one device a user's query names.
package resolver

import (
	"slices"
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// Resolve returns the device that query names. Query may be, in order of
// precedence:
//
//  1. a device ID;
//  2. a Bluetooth address of any of the device's endpoints, in any common
//     notation;
//  3. an exact name;
//  4. a case-insensitive name;
//  5. a case-insensitive substring of a name.
//
// The first step with a match decides. When that step matches more than one
// device, Resolve reports device_ambiguous instead of choosing, unless every
// other match is a known possible match of one paired or connected device.
func Resolve(devices []domain.Device, query string) (domain.Device, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return domain.Device{}, domain.NewError(domain.CodeUsage, "a device is required")
	}
	steps := []func(domain.Device) bool{
		func(d domain.Device) bool { return d.ID == query },
		func(d domain.Device) bool { return hasAddress(d, NormalizeAddress(query)) },
		func(d domain.Device) bool { return d.Name == query },
		func(d domain.Device) bool { return strings.EqualFold(d.Name, query) },
		func(d domain.Device) bool { return strings.Contains(strings.ToLower(d.Name), strings.ToLower(query)) },
	}
	for _, matches := range steps {
		var found []domain.Device
		for _, d := range devices {
			if matches(d) {
				found = append(found, d)
			}
		}
		switch {
		case len(found) == 1:
			return found[0], nil
		case len(found) > 1:
			if preferred, ok := preferredRelated(found); ok {
				return preferred, nil
			}
			return domain.Device{}, ambiguous(query, found)
		}
	}
	return domain.Device{}, domain.NewError(domain.CodeNotFound, "no device matches %q", query)
}

// NormalizeAddress returns a 48-bit Bluetooth address in upper-case,
// colon-separated form, or "" when value is not one. It accepts colons,
// hyphens, or no separators.
func NormalizeAddress(value string) string {
	hexDigits := strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(value))
	if len(hexDigits) != 12 {
		return ""
	}
	var b strings.Builder
	for i, r := range strings.ToUpper(hexDigits) {
		if !strings.ContainsRune("0123456789ABCDEF", r) {
			return ""
		}
		if i > 0 && i%2 == 0 {
			b.WriteByte(':')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasAddress(d domain.Device, address string) bool {
	if address == "" {
		return false
	}
	for _, endpoint := range d.Endpoints {
		if NormalizeAddress(endpoint.Address) == address {
			return true
		}
	}
	return false
}

// preferredRelated returns the paired or connected match that every other
// match names as a possible match of the same physical device.
func preferredRelated(matches []domain.Device) (domain.Device, bool) {
	for _, candidate := range matches {
		if !candidate.Paired && !candidate.Connected {
			continue
		}
		allRelated := true
		for _, other := range matches {
			if other.ID != candidate.ID && !slices.Contains(other.PossibleMatches, candidate.ID) {
				allRelated = false
				break
			}
		}
		if allRelated {
			return candidate, true
		}
	}
	return domain.Device{}, false
}

func ambiguous(query string, matches []domain.Device) error {
	details := domain.AmbiguityDetails{Query: query}
	for _, d := range matches {
		details.Matches = append(details.Matches, domain.Match{ID: d.ID, Name: d.Name, Paired: d.Paired, Connected: d.Connected})
	}
	e := domain.NewError(domain.CodeAmbiguous, "%d devices match %q; use a device ID or address", len(matches), query)
	e.Details = details
	return e
}
