package resolver

import (
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func Resolve(devices []domain.Device, query string) (domain.Device, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return domain.Device{}, domain.NewError(domain.CodeUsage, "device is required")
	}
	for _, d := range devices {
		if d.ID == query {
			return d, nil
		}
	}
	for _, d := range devices {
		if d.Name == query {
			return d, nil
		}
	}
	var folded []domain.Device
	for _, d := range devices {
		if strings.EqualFold(d.Name, query) {
			folded = append(folded, d)
		}
	}
	if len(folded) == 1 {
		return folded[0], nil
	}
	if len(folded) > 1 {
		return ambiguous(query, folded)
	}
	q := strings.ToLower(query)
	var partial []domain.Device
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name), q) {
			partial = append(partial, d)
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(partial) > 1 {
		return ambiguous(query, partial)
	}
	return domain.Device{}, domain.NewError(domain.CodeNotFound, "no device matches %q", query)
}

func ambiguous(query string, matches []domain.Device) (domain.Device, error) {
	choices := make([]map[string]string, 0, len(matches))
	for _, d := range matches {
		choices = append(choices, map[string]string{"id": d.ID, "name": d.Name})
	}
	e := domain.NewError(domain.CodeAmbiguous, "multiple devices match %q", query)
	e.Details = map[string]any{"matches": choices}
	return domain.Device{}, e
}
