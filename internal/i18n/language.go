package i18n

import "strings"

func Text(language, es, en string) string {
	if language == "en" {
		return en
	}
	return es
}
func Normalize(language string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "es", "español", "espanol", "spanish":
		return "es", true
	case "en", "english", "inglés", "ingles":
		return "en", true
	}
	return "", false
}
func Name(language string) string {
	if language == "en" {
		return "English"
	}
	return "español"
}
