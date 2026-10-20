package domain

// Match describes one candidate of an ambiguous device query.
type Match struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}

// AmbiguityDetails is the Details of a CodeAmbiguous error.
type AmbiguityDetails struct {
	Query   string  `json:"query"`
	Matches []Match `json:"matches"`
}
