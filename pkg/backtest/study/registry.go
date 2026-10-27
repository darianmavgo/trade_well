package study

import "strings"

var registry = make(map[string]Study)

// Register makes a Study available by its ID.
func Register(s Study) {
	registry[strings.ToLower(s.ID())] = s
}

// Get retrieves a registered Study by its ID.
func Get(id string) (Study, bool) {
	s, exists := registry[strings.ToLower(id)]
	return s, exists
}

// List returns all registered studies.
func List() []Study {
	var studies []Study
	for _, s := range registry {
		studies = append(studies, s)
	}
	return studies
}
